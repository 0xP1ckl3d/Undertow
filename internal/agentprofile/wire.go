package agentprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"undertow/internal/agent"
	"undertow/internal/deployment"
)

// The overlay uses short field names. Display names and descriptive profile
// metadata stay in the server store rather than in the deployed executable.
type wireConfig struct {
	Server                string             `json:"s"`
	Transport             string             `json:"t"`
	Domain                string             `json:"d,omitempty"`
	Fingerprint           string             `json:"f"`
	Credential            []byte             `json:"e"`
	PayloadProfile        string             `json:"q,omitempty"`
	WebSocketPath         string             `json:"w,omitempty"`
	TLSServerName         string             `json:"n,omitempty"`
	TLSInsecureSkipVerify bool               `json:"x,omitempty"`
	AdvertisedRoutes      []string           `json:"r,omitempty"`
	DeniedCapabilities    string             `json:"b,omitempty"`
	Deployment            deployment.Profile `json:"p,omitempty"`
}

type wireEmbedded struct {
	ProfileID  string     `json:"p"`
	ArtifactID string     `json:"a"`
	Config     wireConfig `json:"c"`
}

func encodeEmbedded(e Embedded) ([]byte, error) {
	c := e.Config
	return json.Marshal(wireEmbedded{ProfileID: e.ProfileID, ArtifactID: e.ArtifactID,
		Config: wireConfig{
			Server: c.Server, Transport: c.Transport, Domain: c.Domain,
			Fingerprint: c.Fingerprint, Credential: c.Credential,
			PayloadProfile: c.PayloadProfile, WebSocketPath: c.WebSocketPath,
			TLSServerName: c.TLSServerName, TLSInsecureSkipVerify: c.TLSInsecureSkipVerify,
			AdvertisedRoutes: c.AdvertisedRoutes, DeniedCapabilities: c.DeniedCapabilities,
			Deployment: c.Deployment,
		}})
}

func decodeEmbedded(payload []byte) (Embedded, error) {
	var w wireEmbedded
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&w); err != nil {
		return Embedded{}, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Embedded{}, errors.New("trailing data")
	}
	c := w.Config
	return Embedded{ProfileID: w.ProfileID, ArtifactID: w.ArtifactID,
		Config: agent.Config{
			Version: agent.ConfigVersion, Server: c.Server, Transport: c.Transport,
			Domain: c.Domain, Fingerprint: c.Fingerprint, AuthMode: "artifact",
			Credential: c.Credential, PayloadProfile: c.PayloadProfile,
			WebSocketPath: c.WebSocketPath, TLSServerName: c.TLSServerName,
			TLSInsecureSkipVerify: c.TLSInsecureSkipVerify,
			AdvertisedRoutes:      c.AdvertisedRoutes, DeniedCapabilities: c.DeniedCapabilities, Deployment: c.Deployment,
		}}, nil
}
