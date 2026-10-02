package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	agentruntime "undertow/internal/agent"
	"undertow/internal/agentprofile"
	"undertow/internal/control"
	"undertow/internal/security"
)

type agentDistribution struct {
	store         *agentprofile.Store
	manager       *control.Manager
	authMode      string
	credential    []byte
	retrievalPath string
}

var retrievalPathPattern = regexp.MustCompile(`^/(?:[A-Za-z0-9_-]+/)*$`)

func validRetrievalPath(value string) bool {
	return len(value) <= 128 && retrievalPathPattern.MatchString(value)
}

func (d *agentDistribution) publicPath() string {
	if d.retrievalPath == "" {
		return "/"
	}
	return d.retrievalPath
}

// Profile responses deliberately omit the enrollment secret.
type publicAgentProfile struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	Created               time.Time `json:"created"`
	UndertowVersion       string    `json:"undertow_version"`
	FormatVersion         uint32    `json:"format_version"`
	Server                string    `json:"server"`
	Transport             string    `json:"transport"`
	Domain                string    `json:"domain,omitempty"`
	Fingerprint           string    `json:"fingerprint"`
	AuthMode              string    `json:"auth_mode"`
	PayloadProfile        string    `json:"payload_profile,omitempty"`
	WebSocketPath         string    `json:"websocket_path,omitempty"`
	TLSServerName         string    `json:"tls_server_name,omitempty"`
	TLSInsecureSkipVerify bool      `json:"tls_insecure_skip_verify,omitempty"`
	AdvertisedRoutes      []string  `json:"advertised_routes,omitempty"`
	DeniedCapabilities    string    `json:"denied_capabilities,omitempty"`
}

func publicProfile(p agentprofile.Profile) publicAgentProfile {
	c := p.Config
	return publicAgentProfile{ID: p.ID, Name: p.Name, Created: p.Created, UndertowVersion: p.UndertowVersion,
		FormatVersion: c.Version, Server: c.Server, Transport: c.Transport, Domain: c.Domain,
		Fingerprint: c.Fingerprint, AuthMode: c.AuthMode, PayloadProfile: c.PayloadProfile,
		WebSocketPath: c.WebSocketPath, TLSServerName: c.TLSServerName,
		TLSInsecureSkipVerify: c.TLSInsecureSkipVerify, AdvertisedRoutes: c.AdvertisedRoutes,
		DeniedCapabilities: c.DeniedCapabilities}
}

type profileRequest struct {
	Name                  string    `json:"name"`
	Server                *string   `json:"server,omitempty"`
	Transport             *string   `json:"transport,omitempty"`
	Domain                *string   `json:"domain,omitempty"`
	Fingerprint           *string   `json:"fingerprint,omitempty"`
	AuthMode              *string   `json:"auth_mode,omitempty"`
	Token                 *string   `json:"token,omitempty"`
	Password              *string   `json:"password,omitempty"`
	PayloadProfile        *string   `json:"payload_profile,omitempty"`
	WebSocketPath         *string   `json:"websocket_path,omitempty"`
	TLSServerName         *string   `json:"tls_server_name,omitempty"`
	TLSInsecureSkipVerify *bool     `json:"tls_insecure_skip_verify,omitempty"`
	AdvertisedRoutes      *[]string `json:"advertised_routes,omitempty"`
	DeniedCapabilities    *string   `json:"denied_capabilities,omitempty"`
}

func decodeDistributionRequest(r *http.Request, target any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, agentprofile.MaxProfileSize+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected request data")
	}
	return nil
}

func (d *agentDistribution) defaults() agentruntime.Config {
	server := d.manager.ServerInfo()
	c := agentruntime.Config{Version: agentruntime.ConfigVersion, Transport: server.Transport, Domain: server.Domain,
		Fingerprint: server.Fingerprint, AuthMode: d.authMode, Credential: append([]byte(nil), d.credential...),
		PayloadProfile: "auto", WebSocketPath: server.WebSocketPath}
	listener := control.ListenerInfo{Transport: server.Transport, Listen: server.Listen, TLSMode: server.TLSMode}
	if len(server.Listeners) > 0 {
		listener = server.Listeners[0]
		for _, kind := range []string{"quic", "websocket", "dns"} {
			found := false
			for _, candidate := range server.Listeners {
				if candidate.Transport == kind {
					listener = candidate
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
	c.Transport = listener.Transport
	if listener.Listen != "" {
		host, port, err := net.SplitHostPort(listener.Listen)
		if err == nil && host != "" && host != "0.0.0.0" && host != "::" {
			c.Server = net.JoinHostPort(host, port)
		}
	}
	if listener.TLSMode == "self-signed" {
		c.TLSInsecureSkipVerify = true
	}
	return c
}

func (d *agentDistribution) apply(c agentruntime.Config, req profileRequest) (agentruntime.Config, error) {
	if req.Server != nil {
		c.Server = *req.Server
	}
	if req.Transport != nil {
		c.Transport = *req.Transport
		for _, listener := range d.manager.ServerInfo().Listeners {
			if listener.Transport != c.Transport {
				continue
			}
			if req.TLSInsecureSkipVerify == nil {
				c.TLSInsecureSkipVerify = listener.TLSMode == "self-signed"
			}
			if req.Server == nil {
				host, _, _ := net.SplitHostPort(c.Server)
				if host == "" || host == "0.0.0.0" || host == "::" {
					host, _, _ = net.SplitHostPort(listener.Listen)
				}
				_, port, err := net.SplitHostPort(listener.Listen)
				if err == nil && host != "" && host != "0.0.0.0" && host != "::" {
					c.Server = net.JoinHostPort(host, port)
				}
			}
			break
		}
	}
	if req.Domain != nil {
		c.Domain = *req.Domain
	}
	if req.Fingerprint != nil {
		fingerprint, err := normalizeFingerprint(*req.Fingerprint)
		if err != nil {
			return c, err
		}
		c.Fingerprint = fingerprint
	}
	if req.AuthMode != nil {
		c.AuthMode = *req.AuthMode
	}
	if req.PayloadProfile != nil {
		c.PayloadProfile = *req.PayloadProfile
	}
	if req.WebSocketPath != nil {
		c.WebSocketPath = *req.WebSocketPath
	}
	if req.TLSServerName != nil {
		c.TLSServerName = *req.TLSServerName
	}
	if req.TLSInsecureSkipVerify != nil {
		c.TLSInsecureSkipVerify = *req.TLSInsecureSkipVerify
	}
	if req.AdvertisedRoutes != nil {
		c.AdvertisedRoutes = append([]string(nil), (*req.AdvertisedRoutes)...)
	}
	if req.DeniedCapabilities != nil {
		c.DeniedCapabilities = *req.DeniedCapabilities
	}
	if req.Token != nil || req.Password != nil || req.AuthMode != nil {
		if req.Token == nil && req.Password == nil && c.AuthMode == d.authMode {
			c.Credential = append([]byte(nil), d.credential...)
		} else {
			token, password := "", ""
			if req.Token != nil {
				token = *req.Token
			}
			if req.Password != nil {
				password = *req.Password
			}
			secret, err := security.EnrollmentSecret(c.AuthMode, "", token, password, "", c.Fingerprint)
			if err != nil {
				return c, err
			}
			c.Credential = secret
		}
	}
	return c, c.Validate()
}

func (d *agentDistribution) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/v1/agent-profiles" && r.Method == http.MethodGet:
		profiles := d.store.Profiles()
		out := make([]publicAgentProfile, 0, len(profiles))
		for _, p := range profiles {
			out = append(out, publicProfile(p))
		}
		distributionJSON(w, http.StatusOK, out)
	case path == "/v1/agent-profiles" && r.Method == http.MethodPost:
		var req profileRequest
		if err := decodeDistributionRequest(r, &req); err != nil {
			distributionError(w, err)
			return
		}
		cfg, err := d.apply(d.defaults(), req)
		if err != nil {
			distributionError(w, err)
			return
		}
		p, err := d.store.Create(req.Name, cfg)
		if err != nil {
			distributionError(w, err)
			return
		}
		distributionJSON(w, http.StatusCreated, publicProfile(p))
	case strings.HasPrefix(path, "/v1/agent-profiles/"):
		name, err := url.PathUnescape(strings.TrimPrefix(path, "/v1/agent-profiles/"))
		if err != nil || strings.Contains(name, "/") {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			p, err := d.store.Profile(name)
			if err != nil {
				distributionError(w, err)
				return
			}
			distributionJSON(w, http.StatusOK, publicProfile(p))
		case http.MethodPut:
			p, err := d.store.Profile(name)
			if err != nil {
				distributionError(w, err)
				return
			}
			var req profileRequest
			if err := decodeDistributionRequest(r, &req); err != nil {
				distributionError(w, err)
				return
			}
			cfg, err := d.apply(p.Config, req)
			if err != nil {
				distributionError(w, err)
				return
			}
			p, err = d.store.Edit(name, cfg)
			if err != nil {
				distributionError(w, err)
				return
			}
			distributionJSON(w, http.StatusOK, publicProfile(p))
		case http.MethodDelete:
			if err := d.store.DeleteProfile(name); err != nil {
				distributionError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case path == "/v1/agent-artifacts" && r.Method == http.MethodGet:
		distributionJSON(w, http.StatusOK, d.store.Artifacts())
	case path == "/v1/agent-artifacts" && r.Method == http.MethodPost:
		var req struct {
			Profile      string `json:"profile"`
			Platform     string `json:"platform"`
			Architecture string `json:"architecture"`
			Filename     string `json:"filename,omitempty"`
		}
		if err := decodeDistributionRequest(r, &req); err != nil {
			distributionError(w, err)
			return
		}
		a, err := d.store.Build(req.Profile, req.Platform, req.Architecture, req.Filename)
		if err != nil {
			distributionError(w, err)
			return
		}
		distributionJSON(w, http.StatusCreated, a)
	case strings.HasPrefix(path, "/v1/agent-artifacts/"):
		parts := strings.Split(strings.TrimPrefix(path, "/v1/agent-artifacts/"), "/")
		id := parts[0]
		if id == "" || len(parts) > 2 {
			http.NotFound(w, r)
			return
		}
		if len(parts) == 1 {
			if r.Method == http.MethodDelete {
				if err := d.store.DeleteArtifact(id); err != nil {
					distributionError(w, err)
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if r.Method == http.MethodGet {
				a, err := d.store.Artifact(id)
				if err != nil {
					distributionError(w, err)
					return
				}
				distributionJSON(w, http.StatusOK, a)
				return
			}
		}
		if len(parts) == 2 && parts[1] == "host" {
			if r.Method == http.MethodGet {
				a, err := d.store.Artifact(id)
				if err != nil {
					distributionError(w, err)
					return
				}
				if !a.Hosted {
					distributionError(w, errors.New("artifact is not hosted"))
					return
				}
				info, err := d.hostedInfo(a)
				if err != nil {
					distributionError(w, err)
					return
				}
				distributionJSON(w, http.StatusOK, info)
				return
			}
			if r.Method == http.MethodPost {
				available := false
				for _, listener := range d.manager.ServerInfo().Listeners {
					if listener.Transport == "websocket" {
						available = true
						break
					}
				}
				if !available {
					distributionError(w, errors.New("artifact hosting requires an active WebSocket HTTPS listener"))
					return
				}
				a, err := d.store.Host(id)
				if err != nil {
					distributionError(w, err)
					return
				}
				info, err := d.hostedInfo(a)
				if err != nil {
					distributionError(w, err)
					return
				}
				distributionJSON(w, http.StatusOK, info)
				return
			}
			if r.Method == http.MethodDelete {
				a, err := d.store.Unhost(id)
				if err != nil {
					distributionError(w, err)
					return
				}
				distributionJSON(w, http.StatusOK, a)
				return
			}
		}
		if len(parts) == 2 && parts[1] == "revoke" && r.Method == http.MethodPost {
			a, err := d.store.Revoke(id)
			if err != nil {
				distributionError(w, err)
				return
			}
			distributionJSON(w, http.StatusOK, a)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	default:
		http.NotFound(w, r)
	}
}

type hostedArtifactInfo struct {
	agentprofile.Artifact
	Retrieval     string `json:"retrieval"`
	TLSSelfSigned bool   `json:"tls_self_signed,omitempty"`
}

func (d *agentDistribution) hostedInfo(a agentprofile.Artifact) (hostedArtifactInfo, error) {
	token, err := d.store.HostedToken(a.ID)
	if err != nil {
		return hostedArtifactInfo{}, err
	}
	path := d.publicPath() + token
	server := d.manager.ServerInfo()
	for _, l := range server.Listeners {
		if l.Transport != "websocket" {
			continue
		}
		_, port, err := net.SplitHostPort(l.Listen)
		if err != nil {
			continue
		}
		host, _, err := net.SplitHostPort(a.Server)
		if err != nil {
			break
		}
		if host != "" {
			return hostedArtifactInfo{Artifact: a, Retrieval: "https://" + net.JoinHostPort(host, port) + path, TLSSelfSigned: l.TLSMode == "self-signed"}, nil
		}
	}
	return hostedArtifactInfo{Artifact: a, Retrieval: path}, nil
}

func (d *agentDistribution) Retrieve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(r.URL.Path, d.publicPath()) {
		http.NotFound(w, r)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, d.publicPath())
	if len(token) != 48 || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return
	}
	a, f, err := d.store.OpenHosted(token)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	publicName := "file"
	if a.Platform == "windows" {
		publicName += ".exe"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", publicName))
	w.Header().Set("X-Artifact-SHA256", a.SHA256)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, publicName, a.Created, f)
}

func distributionJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func distributionError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, os.ErrNotExist) {
		status = http.StatusNotFound
	}
	http.Error(w, err.Error(), status)
}
