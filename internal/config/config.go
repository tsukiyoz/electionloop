package config

import (
	"fmt"

	"zen/cmd/options/runner"
)

type Config struct {
	ETCD *ETCDConfig
}

type ETCDConfig struct {
	Endpoints []string

	TLSCacert string
	TLSCert   string
	TLSKey    string
}

func NewConfig(opts *runner.Options) (*Config, error) {
	if opts == nil {
		return nil, fmt.Errorf("cliopts is not set")
	}

	cfg := &Config{}
	if opts.ETCD != nil {
		cfg.ETCD = &ETCDConfig{
			Endpoints: opts.ETCD.Endpoints,
			TLSCacert: opts.ETCD.TlsCacert,
			TLSCert:   opts.ETCD.TlsCert,
			TLSKey:    opts.ETCD.TlsKey,
		}
	}

	return cfg, nil
}
