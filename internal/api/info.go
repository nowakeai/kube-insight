package api

import "time"

type ServerInfo struct {
	CheckedAt  time.Time                      `json:"checkedAt"`
	Storage    ServerStorageInfo              `json:"storage"`
	Components map[string]ServerComponentInfo `json:"components"`
	Chat       ServerChatInfo                 `json:"chat"`
}

type ServerStorageInfo struct {
	Driver string `json:"driver"`
	Target string `json:"target"`
}

type ServerComponentInfo struct {
	Enabled bool   `json:"enabled"`
	Listen  string `json:"listen,omitempty"`
	URL     string `json:"url,omitempty"`
}

type ServerChatInfo struct {
	Enabled          bool   `json:"enabled"`
	Provider         string `json:"provider,omitempty"`
	Model            string `json:"model,omitempty"`
	APIKeyEnv        string `json:"apiKeyEnv,omitempty"`
	APIKeyConfigured bool   `json:"apiKeyConfigured"`
}

func normalizeServerInfo(info ServerInfo, dbPath string) ServerInfo {
	if info.CheckedAt.IsZero() {
		info.CheckedAt = time.Now().UTC()
	}
	if info.Storage.Driver == "" {
		info.Storage.Driver = "sqlite"
	}
	if info.Storage.Target == "" {
		info.Storage.Target = dbPath
	}
	if info.Components == nil {
		info.Components = map[string]ServerComponentInfo{
			"api": {Enabled: true},
		}
	}
	return info
}
