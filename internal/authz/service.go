package authz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	bootstrapClientID = "host-agent-bootstrap"
	oputeClientID     = "opute-mcp-host"
	accessTokenTTL    = time.Hour
	authCodeTTL       = 10 * time.Minute
)

type Service struct {
	store          *Store
	bootstrapToken string
	httpClient     *http.Client
}

type Options struct {
	StateDir       string
	BootstrapToken string
	OputeSecret    string
	HTTPClient     *http.Client
}

type Decision struct {
	Allowed      bool
	Status       int
	WWWAuth      string
	Insufficient bool
}

func Open(opts Options) (*Service, error) {
	store, err := OpenStore(opts.StateDir)
	if err != nil {
		return nil, err
	}
	svc := &Service{
		store:          store,
		bootstrapToken: strings.TrimSpace(opts.BootstrapToken),
		httpClient:     opts.HTTPClient,
	}
	if svc.httpClient == nil {
		svc.httpClient = &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	}
	if err := store.upsertClient(registeredClient{
		ClientID: bootstrapClientID, ClientType: "bootstrap", Confidential: true,
		RedirectURIs: []string{"http://127.0.0.1/callback", "http://localhost/callback"},
	}); err != nil {
		_ = store.Close()
		return nil, err
	}
	opute := registeredClient{
		ClientID: oputeClientID, ClientType: "confidential", Confidential: true,
		RedirectURIs: []string{"https://127.0.0.1/oauth/callback"},
	}
	if secret := strings.TrimSpace(opts.OputeSecret); secret != "" {
		opute.SecretHash = hashToken(secret)
	}
	if err := store.upsertClient(opute); err != nil {
		_ = store.Close()
		return nil, err
	}
	return svc, nil
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	return s.store.Close()
}

func (s *Service) HasProtection() bool {
	return s != nil
}

func (s *Service) Authorize(r *http.Request) Decision {
	if s == nil {
		return Decision{Status: http.StatusUnauthorized, WWWAuth: s.wwwAuthenticate(r)}
	}
	token := bearerToken(r)
	resource := CanonicalMCPResource(r)
	if token == "" {
		return Decision{Status: http.StatusUnauthorized, WWWAuth: s.wwwAuthenticate(r)}
	}
	if s.bootstrapToken != "" && constantEquals(token, s.bootstrapToken) {
		if !IsLocalHostAddress(r.Host) {
			return Decision{Status: http.StatusForbidden, Insufficient: true, WWWAuth: s.wwwAuthenticate(r)}
		}
		return Decision{Allowed: true, Status: http.StatusOK}
	}
	record, ok, err := s.store.tokenByHash(hashToken(token))
	if err != nil || !ok || record.Revoked || time.Now().Unix() > record.ExpiresAt {
		return Decision{Status: http.StatusUnauthorized, WWWAuth: s.wwwAuthenticate(r)}
	}
	if record.Resource != resource {
		return Decision{Status: http.StatusForbidden, Insufficient: true, WWWAuth: s.wwwAuthenticate(r)}
	}
	if record.Scope != "" && record.Scope != MCPScope {
		return Decision{Status: http.StatusForbidden, Insufficient: true, WWWAuth: s.wwwAuthenticate(r)}
	}
	return Decision{Allowed: true, Status: http.StatusOK}
}

func (s *Service) wwwAuthenticate(r *http.Request) string {
	metadata := requestOrigin(r) + ProtectedResourceMetadataPath(r)
	return fmt.Sprintf(`Bearer resource_metadata=%q, scope=%q`, metadata, MCPScope)
}

func (s *Service) RegisterHTTP(mux *http.ServeMux) {
	if s == nil || mux == nil {
		return
	}
	mux.HandleFunc("/.well-known/oauth-protected-resource", s.handlePRM)
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", s.handlePRM)
	mux.HandleFunc(AuthorizationServerMetadataPath(), s.handleASMetadata)
	mux.HandleFunc("/oauth/authorize", s.handleAuthorize)
	mux.HandleFunc("/oauth/token", s.handleToken)
	mux.HandleFunc("/oauth/revoke", s.handleRevoke)
}

func (s *Service) handlePRM(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	issuer := requestOrigin(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 CanonicalMCPResource(r),
		"authorization_servers":    []string{issuer},
		"scopes_supported":         []string{MCPScope},
		"bearer_methods_supported": []string{"header"},
		"resource_documentation":   issuer + "/mcp",
	})
}

func (s *Service) handleASMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	issuer := requestOrigin(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         issuer,
		"authorization_endpoint":                         issuer + "/oauth/authorize",
		"token_endpoint":                                 issuer + "/oauth/token",
		"revocation_endpoint":                            issuer + "/oauth/revoke",
		"code_challenge_methods_supported":               []string{"S256"},
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "client_credentials"},
		"token_endpoint_auth_methods_supported":          []string{"client_secret_basic", "client_secret_post", "none"},
		"authorization_response_iss_parameter_supported": true,
		"client_id_metadata_document_supported":          true,
		"scopes_supported":                               []string{MCPScope},
	})
}

func (s *Service) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	clientID := strings.TrimSpace(firstForm(r, "client_id"))
	redirectURI := strings.TrimSpace(firstForm(r, "redirect_uri"))
	resource := strings.TrimSpace(firstForm(r, "resource"))
	challenge := strings.TrimSpace(firstForm(r, "code_challenge"))
	method := strings.TrimSpace(firstForm(r, "code_challenge_method"))
	state := firstForm(r, "state")
	if firstForm(r, "response_type") != "code" || clientID == "" || redirectURI == "" || resource == "" || challenge == "" || !strings.EqualFold(method, "S256") {
		http.Error(w, "invalid_request", http.StatusBadRequest)
		return
	}
	if !validNativeRedirect(redirectURI) {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	client, err := s.resolveClient(r.Context(), clientID, redirectURI)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	code, err := randomCode()
	if err != nil {
		http.Error(w, "server_error", http.StatusInternalServerError)
		return
	}
	if err := s.store.saveCode(authCode{
		Code: code, ClientID: client.ClientID, Resource: resource,
		CodeChallenge: challenge, RedirectURI: redirectURI,
		ExpiresAt: time.Now().Add(authCodeTTL).Unix(),
	}); err != nil {
		http.Error(w, "server_error", http.StatusInternalServerError)
		return
	}
	target, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	query := target.Query()
	query.Set("code", code)
	query.Set("iss", requestOrigin(r))
	if state != "" {
		query.Set("state", state)
	}
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *Service) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed body")
		return
	}
	grant := firstForm(r, "grant_type")
	resource := strings.TrimSpace(firstForm(r, "resource"))
	clientID, clientSecret := clientAuth(r)
	switch grant {
	case "authorization_code":
		s.issueAuthorizationCodeToken(w, r, clientID, resource)
	case "client_credentials":
		s.issueClientCredentialsToken(w, clientID, clientSecret, resource)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "use authorization_code or client_credentials")
	}
}

func (s *Service) issueAuthorizationCodeToken(w http.ResponseWriter, r *http.Request, clientID, resource string) {
	code, err := s.store.consumeCode(firstForm(r, "code"))
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", err.Error())
		return
	}
	if clientID != "" && clientID != code.ClientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "client mismatch")
		return
	}
	if resource != "" && resource != code.Resource {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "resource mismatch")
		return
	}
	if firstForm(r, "redirect_uri") != "" && firstForm(r, "redirect_uri") != code.RedirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
		return
	}
	if s256(firstForm(r, "code_verifier")) != code.CodeChallenge {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "pkce verification failed")
		return
	}
	s.writeAccessToken(w, code.ClientID, code.Resource)
}

func (s *Service) issueClientCredentialsToken(w http.ResponseWriter, clientID, clientSecret, resource string) {
	if resource == "" || clientID == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id and resource are required")
		return
	}
	client, ok, err := s.store.client(clientID)
	if err != nil || !ok || !client.Confidential {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "unknown confidential client")
		return
	}
	if client.SecretHash != "" && hashToken(clientSecret) != client.SecretHash {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		return
	}
	s.writeAccessToken(w, client.ClientID, resource)
}

func (s *Service) writeAccessToken(w http.ResponseWriter, clientID, resource string) {
	token, err := randomToken()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "token mint failed")
		return
	}
	if err := s.store.saveToken(issuedToken{
		Hash: hashToken(token), ClientID: clientID, Resource: resource, Scope: MCPScope,
		ExpiresAt: time.Now().Add(accessTokenTTL).Unix(),
	}); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "token persist failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(accessTokenTTL.Seconds()),
		"scope":        MCPScope,
		"resource":     resource,
	})
}

func (s *Service) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	token := firstForm(r, "token")
	if token != "" {
		_ = s.store.revokeHash(hashToken(token))
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Service) resolveClient(ctx context.Context, clientID, redirectURI string) (registeredClient, error) {
	if client, ok, err := s.store.client(clientID); err != nil {
		return registeredClient{}, err
	} else if ok {
		if len(client.RedirectURIs) > 0 && !containsString(client.RedirectURIs, redirectURI) && !strings.HasPrefix(redirectURI, "http://127.0.0.1") && !strings.HasPrefix(redirectURI, "http://localhost") {
			return registeredClient{}, fmt.Errorf("redirect_uri is not registered")
		}
		return client, nil
	}
	if !strings.HasPrefix(clientID, "https://") {
		return registeredClient{}, fmt.Errorf("unknown client_id")
	}
	client, err := fetchCIMD(ctx, s.httpClient, clientID)
	if err != nil {
		return registeredClient{}, err
	}
	if !containsString(client.RedirectURIs, redirectURI) {
		return registeredClient{}, fmt.Errorf("redirect_uri is not registered")
	}
	_ = s.store.upsertClient(client)
	return client, nil
}

func fetchCIMD(ctx context.Context, client *http.Client, clientID string) (registeredClient, error) {
	parsed, err := url.Parse(clientID)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return registeredClient{}, fmt.Errorf("client_id must be an https metadata URL")
	}
	if err := rejectSSRF(parsed.Hostname()); err != nil {
		return registeredClient{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clientID, nil)
	if err != nil {
		return registeredClient{}, err
	}
	res, err := client.Do(req)
	if err != nil {
		return registeredClient{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return registeredClient{}, fmt.Errorf("client metadata fetch failed")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return registeredClient{}, err
	}
	var doc struct {
		ClientID     string   `json:"client_id"`
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return registeredClient{}, err
	}
	if doc.ClientID != clientID {
		return registeredClient{}, fmt.Errorf("client_id does not match metadata URL")
	}
	if len(doc.RedirectURIs) == 0 {
		return registeredClient{}, fmt.Errorf("client metadata has no redirect_uris")
	}
	for _, uri := range doc.RedirectURIs {
		if !validNativeRedirect(uri) {
			return registeredClient{}, fmt.Errorf("redirect_uri must be localhost or https")
		}
	}
	return registeredClient{
		ClientID: clientID, ClientType: "public", RedirectURIs: doc.RedirectURIs, MetadataURL: clientID,
	}, nil
}

func rejectSSRF(host string) error {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("client metadata host is not allowed")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("client metadata host is not resolvable")
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("client metadata host is not allowed")
		}
		if ip.Equal(net.ParseIP("169.254.169.254")) {
			return fmt.Errorf("client metadata host is not allowed")
		}
	}
	return nil
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
}

func requestOrigin(r *http.Request) string {
	return requestScheme(r) + "://" + r.Host
}

func firstForm(r *http.Request, key string) string {
	if r.Form != nil {
		if value := strings.TrimSpace(r.Form.Get(key)); value != "" {
			return value
		}
	}
	return strings.TrimSpace(r.URL.Query().Get(key))
}

func clientAuth(r *http.Request) (string, string) {
	if user, secret, ok := r.BasicAuth(); ok {
		return user, secret
	}
	return firstForm(r, "client_id"), firstForm(r, "client_secret")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, map[string]any{"error": code, "error_description": description})
}
