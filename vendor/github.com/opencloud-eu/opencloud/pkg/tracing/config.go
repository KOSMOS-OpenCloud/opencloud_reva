package tracing

// ConfigConverter is the interface for external configuration.
type ConfigConverter interface {
	Convert() Config
}

// Tracing defines the available tracing configuration.
type Config struct {
	Enabled   bool   `yaml:"enabled" env:"OC_TRACING_ENABLED" desc:"Activates tracing." introductionVersion:"1.0.0"`
	Type      string `yaml:"type" env:"OC_TRACING_TYPE" desc:"The type of tracing. Defaults to \"\", which is the same as \"jaeger\". Allowed tracing types are \"jaeger\" and \"\" as of now." introductionVersion:"1.0.0"`
	Endpoint  string `yaml:"endpoint" env:"OC_TRACING_ENDPOINT" desc:"The endpoint of the tracing agent." introductionVersion:"1.0.0"`
	Collector string `yaml:"collector" env:"OC_TRACING_COLLECTOR" desc:"The HTTP endpoint for sending spans directly to a collector, i.e. http://jaeger-collector:14268/api/traces. Only used if the tracing endpoint is unset." introductionVersion:"1.0.0"`
}
