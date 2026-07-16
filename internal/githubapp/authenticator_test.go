package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/golang-jwt/jwt/v4"
)

const testAppID = 123

// testKey generates an RSA key pair and returns the private key plus its
// PKCS#1 PEM encoding (the format GitHub issues for GitHub Apps).
func testKey(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return key, pemBytes
}

// fakeGitHub is an httptest server that mimics the GitHub App endpoints used by
// the authenticator: installation resolution, token minting, and one data
// endpoint used to prove installation-authenticated requests.
type fakeGitHub struct {
	*httptest.Server
	pub *rsa.PublicKey

	tokenDelay time.Duration // artificial delay before minting tokens

	mu               sync.Mutex
	tokenCalls       map[int64]int // installation id -> POST access_tokens count
	dataAuthHeader   string        // Authorization seen on the data endpoint
	dataRequestCount int
}

func newFakeGitHub(t *testing.T, pub *rsa.PublicKey) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{pub: pub, tokenCalls: map[int64]int{}}

	// installation resolution: owner/org/user -> installation id (or 404).
	repoInstalls := map[string]int64{
		"octo-org/hello": 456,
		"boom-org/boom":  999, // token minting returns 500
		"slow-org/slow":  888, // token minting is delayed
	}
	orgInstalls := map[string]int64{"octo-org": 789}
	userInstalls := map[string]int64{"octocat": 321}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /repos/{owner}/{repo}/installation", func(w http.ResponseWriter, r *http.Request) {
		f.requireAppJWT(t, w, r)
		key := r.PathValue("owner") + "/" + r.PathValue("repo")
		id, ok := repoInstalls[key]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, fmt.Sprintf(`{"id":%d}`, id))
	})

	mux.HandleFunc("GET /orgs/{org}/installation", func(w http.ResponseWriter, r *http.Request) {
		f.requireAppJWT(t, w, r)
		id, ok := orgInstalls[r.PathValue("org")]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, fmt.Sprintf(`{"id":%d}`, id))
	})

	mux.HandleFunc("GET /users/{user}/installation", func(w http.ResponseWriter, r *http.Request) {
		f.requireAppJWT(t, w, r)
		id, ok := userInstalls[r.PathValue("user")]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, fmt.Sprintf(`{"id":%d}`, id))
	})

	mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		f.requireAppJWT(t, w, r)
		var id int64
		fmt.Sscanf(r.PathValue("id"), "%d", &id)
		f.mu.Lock()
		f.tokenCalls[id]++
		f.mu.Unlock()
		switch id {
		case 999:
			http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
			return
		case 888:
			if f.tokenDelay > 0 {
				time.Sleep(f.tokenDelay)
			}
		}
		expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		writeJSON(w, http.StatusCreated, fmt.Sprintf(
			`{"token":"ghs_token_%d","expires_at":%q,"permissions":{"contents":"read"}}`, id, expires))
	})

	// list installations (app-authenticated), returned across two pages to
	// exercise pagination.
	mux.HandleFunc("GET /app/installations", func(w http.ResponseWriter, r *http.Request) {
		f.requireAppJWT(t, w, r)
		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s/app/installations?page=2>; rel="next"`, f.URL))
			writeJSON(w, http.StatusOK,
				`[{"id":456,"target_type":"Organization","repository_selection":"all","account":{"login":"octo-org"}}]`)
		case "2":
			writeJSON(w, http.StatusOK,
				`[{"id":321,"target_type":"User","repository_selection":"selected","account":{"login":"octocat"}}]`)
		default:
			writeJSON(w, http.StatusOK, `[]`)
		}
	})

	// A data endpoint used to verify installation-authenticated requests.
	mux.HandleFunc("GET /repos/{owner}/{repo}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.dataAuthHeader = r.Header.Get("Authorization")
		f.dataRequestCount++
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, fmt.Sprintf(`{"id":1,"name":%q}`, r.PathValue("repo")))
	})

	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Server.Close)
	return f
}

func (f *fakeGitHub) tokenCallCount(id int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokenCalls[id]
}

// requireAppJWT asserts the request carries a valid app JWT (Bearer, RS256,
// signed by our key, with the expected issuer and time claims).
func (f *fakeGitHub) requireAppJWT(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "Bearer ") {
		t.Errorf("expected Bearer app JWT, got %q", authz)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	raw := strings.TrimPrefix(authz, "Bearer ")

	var claims jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(raw, &claims, func(tok *jwt.Token) (interface{}, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", tok.Header["alg"])
		}
		return f.pub, nil
	})
	if err != nil {
		t.Errorf("invalid app JWT: %v", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if claims.Issuer != fmt.Sprint(testAppID) {
		t.Errorf("JWT iss = %q, want %q", claims.Issuer, fmt.Sprint(testAppID))
	}
	now := time.Now()
	switch {
	case claims.ExpiresAt == nil || claims.ExpiresAt.Before(now):
		t.Error("JWT exp is missing or already expired")
	case claims.ExpiresAt.After(now.Add(11 * time.Minute)):
		t.Error("JWT exp is more than 10 minutes in the future")
	}
	if claims.IssuedAt == nil || claims.IssuedAt.After(now.Add(time.Minute)) {
		t.Error("JWT iat is missing or in the future")
	}
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func newTestAuthenticator(t *testing.T) (*Authenticator, *fakeGitHub) {
	t.Helper()
	return newTestAuthenticatorWithTimeout(t, 0)
}

func newTestAuthenticatorWithTimeout(t *testing.T, timeout time.Duration) (*Authenticator, *fakeGitHub) {
	t.Helper()
	key, pemBytes := testKey(t)
	fake := newFakeGitHub(t, &key.PublicKey)

	auth, err := New(Config{AppID: testAppID, PrivateKey: pemBytes, APIBaseURL: fake.URL, Timeout: timeout})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return auth, fake
}

func TestAuthenticator_RepositoryTokenAndCaching(t *testing.T) {
	auth, fake := newTestAuthenticator(t)
	ctx := context.Background()

	tok, err := auth.RepositoryToken(ctx, "octo-org", "hello")
	if err != nil {
		t.Fatalf("RepositoryToken: %v", err)
	}
	if tok != "ghs_token_456" {
		t.Errorf("token = %q, want %q", tok, "ghs_token_456")
	}

	// A second call should reuse the cached installation token (no new POST to
	// the access_tokens endpoint), while resolution may happen again.
	if _, err := auth.RepositoryToken(ctx, "octo-org", "hello"); err != nil {
		t.Fatalf("second RepositoryToken: %v", err)
	}
	if got := fake.tokenCallCount(456); got != 1 {
		t.Errorf("access_tokens minted %d times, want 1 (token should be cached)", got)
	}
}

func TestAuthenticator_OrganizationToken(t *testing.T) {
	auth, _ := newTestAuthenticator(t)

	tok, err := auth.OrganizationToken(context.Background(), "octo-org")
	if err != nil {
		t.Fatalf("OrganizationToken: %v", err)
	}
	if tok != "ghs_token_789" {
		t.Errorf("token = %q, want %q", tok, "ghs_token_789")
	}
}

func TestAuthenticator_RepositoryClientIsInstallationAuthenticated(t *testing.T) {
	auth, fake := newTestAuthenticator(t)
	ctx := context.Background()

	client, err := auth.RepositoryClient(ctx, "octo-org", "hello")
	if err != nil {
		t.Fatalf("RepositoryClient: %v", err)
	}

	repo, _, err := client.Repositories.Get(ctx, "octo-org", "hello")
	if err != nil {
		t.Fatalf("Repositories.Get: %v", err)
	}
	if repo.GetName() != "hello" {
		t.Errorf("repo name = %q, want %q", repo.GetName(), "hello")
	}

	fake.mu.Lock()
	authz := fake.dataAuthHeader
	fake.mu.Unlock()
	if !strings.Contains(authz, "ghs_token_456") {
		t.Errorf("data request Authorization = %q, want it to carry the installation token", authz)
	}
}

func TestAuthenticator_ResolutionError(t *testing.T) {
	auth, _ := newTestAuthenticator(t)

	if _, err := auth.RepositoryToken(context.Background(), "ghost", "missing"); err == nil {
		t.Fatal("expected an error resolving an unknown installation")
	}
}

func TestAuthenticator_Installations(t *testing.T) {
	auth, _ := newTestAuthenticator(t)

	insts, err := auth.Installations(context.Background())
	if err != nil {
		t.Fatalf("Installations: %v", err)
	}
	if len(insts) != 2 {
		t.Fatalf("got %d installations, want 2 (across two pages)", len(insts))
	}
	if insts[0] != (Installation{ID: 456, Account: "octo-org", AccountType: "Organization", RepositorySelection: "all"}) {
		t.Errorf("insts[0] = %+v", insts[0])
	}
	if insts[1] != (Installation{ID: 321, Account: "octocat", AccountType: "User", RepositorySelection: "selected"}) {
		t.Errorf("insts[1] = %+v", insts[1])
	}
}

func TestAuthenticator_TokenMintFailure(t *testing.T) {
	auth, _ := newTestAuthenticator(t)

	// Installation 999's token endpoint returns 500; ghinstallation surfaces an
	// *HTTPError, which must propagate as an error.
	if _, err := auth.RepositoryToken(context.Background(), "boom-org", "boom"); err == nil {
		t.Fatal("expected an error when token minting fails")
	}
}

func TestAuthenticator_TokenMintTimeout(t *testing.T) {
	auth, fake := newTestAuthenticatorWithTimeout(t, 100*time.Millisecond)
	fake.tokenDelay = 750 * time.Millisecond

	_, err := auth.RepositoryToken(context.Background(), "slow-org", "slow")
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestAuthenticator_ConcurrentTokens(t *testing.T) {
	auth, _ := newTestAuthenticator(t)

	var wg sync.WaitGroup
	errCh := make(chan error, 40)
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := auth.RepositoryToken(context.Background(), "octo-org", "hello"); err != nil {
				errCh <- err
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := auth.OrganizationToken(context.Background(), "octo-org"); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent token error: %v", err)
	}
}

func TestAuthenticator_RefusesCrossHostRedirect(t *testing.T) {
	_, pemBytes := testKey(t)

	// The secondary host records whether it ever receives an Authorization
	// header — it must not, because the redirect should be refused first.
	var leaked int32
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			atomic.AddInt32(&leaked, 1)
		}
		writeJSON(w, http.StatusOK, `{"id":1}`)
	}))
	t.Cleanup(secondary.Close)

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secondary.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(primary.Close)

	auth, err := New(Config{AppID: testAppID, PrivateKey: pemBytes, APIBaseURL: primary.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := auth.RepositoryToken(context.Background(), "octo-org", "hello"); err == nil {
		t.Fatal("expected the cross-host redirect to be refused")
	}
	if n := atomic.LoadInt32(&leaked); n != 0 {
		t.Errorf("credentials were sent to the redirect target %d time(s)", n)
	}
}

// trackingBody is an io.ReadCloser that records whether it was closed.
type trackingBody struct {
	io.Reader
	closed bool
}

func (b *trackingBody) Close() error {
	b.closed = true
	return nil
}

func TestDrainAndCloseHTTPError(t *testing.T) {
	body := &trackingBody{Reader: strings.NewReader("error body")}
	httpErr := &ghinstallation.HTTPError{
		Message:  "boom",
		Response: &http.Response{Body: body},
	}

	// Wrapped, as the authenticator receives it.
	drainAndCloseHTTPError(fmt.Errorf("minting token: %w", httpErr))

	if !body.closed {
		t.Error("expected the HTTPError response body to be closed")
	}
}
