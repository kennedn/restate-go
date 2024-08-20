package config

type Config struct {
	ApiVersion string    `yaml:"apiVersion"`
	Devices    []Devices `yaml:"devices"`
}

type Devices struct {
	Type   string         `yaml:"type"`
	Config map[string]any `yaml:"config"`
}
