package model

// Go models that match the resume.schema.json used for validation and rendering.

type Meta struct {
	Name     string            `json:"name"`
	Headline string            `json:"headline"`
	Contact  map[string]string `json:"contact,omitempty"`
}

type Snapshot struct {
	Tech             string   `json:"tech"`
	Achievements     []string `json:"achievements"`
	SelectedProjects []string `json:"selected_projects"`
}

type Role struct {
	Company string   `json:"company"`
	Title   string   `json:"title"`
	Period  string   `json:"period,omitempty"`
	Bullets []string `json:"bullets,omitempty"`
}

type Project struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	URL         string   `json:"url,omitempty"`
	Stack       string   `json:"stack,omitempty"`
	Description string   `json:"description"`
	Bullets     []string `json:"bullets,omitempty"`
}

type Resume struct {
	Meta           Meta                  `json:"meta"`
	Summary        string                `json:"summary"`
	Snapshot       Snapshot              `json:"snapshot"`
	Experience     []Role                `json:"experience"`
	Projects       []Project             `json:"projects"`
	Publications   []string              `json:"publications,omitempty"`
	Certifications []string              `json:"certifications,omitempty"`
	Extras         string                `json:"extras,omitempty"`
	Labels         map[string]string     `json:"labels,omitempty"`
}
