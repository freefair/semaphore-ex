package util

type App struct {
	// Active controls this configured app's UI availability. Explicit map
	// entries default to false; auto-discovered installed tools are added active.
	Active    bool     `json:"active"`
	Priority  int      `json:"priority"`
	Title     string   `json:"title"`
	Icon      string   `json:"icon"`
	Color     string   `json:"color"`
	DarkColor string   `json:"dark_color"`
	AppPath   string   `json:"path"`
	AppArgs   []string `json:"args"`
}
