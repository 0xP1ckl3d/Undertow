package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"time"

	"undertow/internal/agentprofile"
	"undertow/internal/mux"
	"undertow/internal/namedpipe"
	"undertow/internal/pivot"
)

// The active relay listener serves both child sessions and explicit, tokened
// HTTPS downloads. The parent fetches each artifact through its authenticated
// Undertow stream. No artifact bytes or server control API reside on the agent.
type agentPayloadHostInfo struct {
	ID              string    `json:"id"`
	ArtifactID      string    `json:"artifact_id"`
	AgentID         string    `json:"agent_id"`
	Bind            string    `json:"bind"`
	PublicHost      string    `json:"public_host"`
	Retrieval       string    `json:"retrieval"`
	RetrievalPath   string    `json:"retrieval_path"`
	PipePath        string    `json:"pipe_path,omitempty"`
	TLSSelfSigned   bool      `json:"tls_self_signed"`
	TLSCertSHA256   string    `json:"tls_cert_sha256"`
	TLSPublicKeyPin string    `json:"tls_public_key_pin"`
	Started         time.Time `json:"started"`
}

type agentPayloadHost struct {
	info  agentPayloadHostInfo
	token string
}

func (d *agentDistribution) agentHostList(artifactID string) []agentPayloadHostInfo {
	d.hostsMu.RLock()
	defer d.hostsMu.RUnlock()
	out := make([]agentPayloadHostInfo, 0, len(d.agentHosts))
	for _, host := range d.agentHosts {
		if artifactID == "" || host.info.ArtifactID == artifactID {
			out = append(out, host.info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

func (d *agentDistribution) agentHost(id string) (agentPayloadHostInfo, bool) {
	d.hostsMu.RLock()
	defer d.hostsMu.RUnlock()
	host := d.agentHosts[id]
	if host == nil {
		return agentPayloadHostInfo{}, false
	}
	return host.info, true
}

func (d *agentDistribution) startAgentPayloadHost(ctx context.Context, artifactID, agentID, bind, publicHost string) (agentPayloadHostInfo, error) {
	if agentID == "" || bind == "" || publicHost == "" || (!validRetrievalHost(publicHost) && !(namedpipe.IsLocal(bind) && publicHost == ".")) {
		return agentPayloadHostInfo{}, errors.New("connected parent agent, active relay bind, and child-reachable host are required")
	}
	a, err := d.store.Artifact(artifactID)
	if err != nil {
		return agentPayloadHostInfo{}, err
	}
	if a.Revoked {
		return agentPayloadHostInfo{}, errors.New("revoked artifact cannot be hosted")
	}
	if namedpipe.IsLocal(bind) && a.Platform != "windows" {
		return agentPayloadHostInfo{}, errors.New("SMB named-pipe delivery requires a Windows payload")
	}
	if err := agentprofile.VerifyFile(d.store.ArtifactPath(a), a.SHA256); err != nil {
		return agentPayloadHostInfo{}, fmt.Errorf("verify artifact before hosting: %w", err)
	}
	id, err := agentprofile.ID()
	if err != nil {
		return agentPayloadHostInfo{}, err
	}
	first, err := agentprofile.ID()
	if err != nil {
		return agentPayloadHostInfo{}, err
	}
	second, err := agentprofile.ID()
	if err != nil {
		return agentPayloadHostInfo{}, err
	}
	token := first + second
	result, done, err := d.manager.SetRelayPayload(ctx, agentID, bind, token, true)
	if err != nil {
		return agentPayloadHostInfo{}, err
	}
	info := agentPayloadHostInfo{ID: id, ArtifactID: a.ID, AgentID: agentID, Bind: bind, PublicHost: publicHost, RetrievalPath: "/" + token, TLSSelfSigned: true, TLSCertSHA256: result.TLSCertSHA256, TLSPublicKeyPin: result.TLSPublicKeyPin, Started: time.Now().UTC()}
	if namedpipe.IsLocal(bind) {
		pipeName := bind[len(`\\.\pipe\`):]
		info.PipePath = `\\` + publicHost + `\pipe\` + pipeName
		if err := namedpipe.ValidateRemote(info.PipePath); err != nil {
			_, _, _ = d.manager.SetRelayPayload(context.Background(), agentID, bind, token, false)
			return agentPayloadHostInfo{}, err
		}
		info.Retrieval = "smb-pipe://" + publicHost + "/" + pipeName + "/" + token
	} else {
		_, port, err := net.SplitHostPort(bind)
		if err != nil {
			_, _, _ = d.manager.SetRelayPayload(context.Background(), agentID, bind, token, false)
			return agentPayloadHostInfo{}, err
		}
		info.Retrieval = "https://" + net.JoinHostPort(publicHost, port) + "/" + token
	}
	d.hostsMu.Lock()
	if d.agentHosts == nil {
		d.agentHosts = make(map[string]*agentPayloadHost)
	}
	host := &agentPayloadHost{info: info, token: token}
	d.agentHosts[id] = host
	d.hostsMu.Unlock()
	d.manager.PublishEvent("payload.agent_host.changed", id)
	go func() { <-done; d.stopAgentPayloadHost(id) }()
	return info, nil
}

func (d *agentDistribution) stopAgentPayloadHost(id string) bool {
	d.hostsMu.Lock()
	host := d.agentHosts[id]
	if host != nil {
		delete(d.agentHosts, id)
	}
	d.hostsMu.Unlock()
	if host == nil {
		return false
	}
	_, _, _ = d.manager.SetRelayPayload(context.Background(), host.info.AgentID, host.info.Bind, host.token, false)
	d.manager.PublishEvent("payload.agent_host.changed", id)
	return true
}

func (d *agentDistribution) stopArtifactHosts(artifactID string) {
	for _, host := range d.agentHostList(artifactID) {
		d.stopAgentPayloadHost(host.ID)
	}
}

func (d *agentDistribution) serveRelayPayload(ctx context.Context, agentID string, stream *mux.Stream) {
	defer stream.Close()
	var request pivot.RelayPayloadRequest
	if err := json.NewDecoder(io.LimitReader(stream, 1024)).Decode(&request); err != nil {
		return
	}
	var artifactID string
	d.hostsMu.RLock()
	for _, host := range d.agentHosts {
		if host.info.AgentID == agentID && subtle.ConstantTimeCompare([]byte(host.token), []byte(request.Token)) == 1 {
			artifactID = host.info.ArtifactID
			break
		}
	}
	d.hostsMu.RUnlock()
	if artifactID == "" {
		_ = json.NewEncoder(stream).Encode(pivot.RelayPayloadHeader{Error: "not found"})
		return
	}
	a, err := d.store.Artifact(artifactID)
	if err != nil || a.Revoked {
		_ = json.NewEncoder(stream).Encode(pivot.RelayPayloadHeader{Error: "not found"})
		return
	}
	path := d.store.ArtifactPath(a)
	if err := agentprofile.VerifyFile(path, a.SHA256); err != nil {
		_ = json.NewEncoder(stream).Encode(pivot.RelayPayloadHeader{Error: "unavailable"})
		return
	}
	file, err := os.Open(path)
	if err != nil {
		_ = json.NewEncoder(stream).Encode(pivot.RelayPayloadHeader{Error: "unavailable"})
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || stat.Size() != a.Size || stat.Size() > 512<<20 {
		_ = json.NewEncoder(stream).Encode(pivot.RelayPayloadHeader{Error: "unavailable"})
		return
	}
	if err := json.NewEncoder(stream).Encode(pivot.RelayPayloadHeader{Size: stat.Size(), SHA256: a.SHA256}); err != nil {
		return
	}
	if request.Head {
		_ = stream.CloseWrite()
		return
	}
	_, _ = io.CopyN(stream, file, stat.Size())
	_ = stream.CloseWrite()
	_ = ctx
}
