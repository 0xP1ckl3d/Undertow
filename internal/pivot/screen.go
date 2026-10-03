package pivot

// ScreenInfo describes one physical display in the agent's desktop session.
type ScreenInfo struct {
	Number     int    `json:"number"`
	Name       string `json:"name"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Foreground string `json:"foreground,omitempty"`
}
