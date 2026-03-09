package runner

import (
	cliflag "zen/pkg/app/flag"

	"github.com/spf13/pflag"
)

type Options struct {
	// HTTP *HTTPOptions
	// GRPC *GRPCOptions
	ETCD   *ETCDOptions
	NodeID string `mapstructure:"node-id"`
}

func NewOptions() *Options {
	return &Options{
		ETCD: &ETCDOptions{},
	}
}

func (s *Options) Flags() (fss cliflag.NamedFlagSets) {
	s.ETCD.AddFlags(fss.FlagSet("etcd"))
	fss.FlagSet("global").StringVar(&s.NodeID, "node-id", "", "node identifier for election")
	return
}

type ETCDOptions struct {
	Endpoints []string `mapstructure:"endpoints"`
	TlsCacert string   `mapstructure:"tls-cacert"`
	TlsCert   string   `mapstructure:"tls-cert"`
	TlsKey    string   `mapstructure:"tls-key"`
}

func (s *ETCDOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringArrayVar(&s.Endpoints, "etcd-endpoints", []string{"127.0.0.1:2379"}, "etcd endpoints")
	fs.StringVar(&s.TlsCacert, "etcd-tls-cacert", "", "etcd tls cacert")
	fs.StringVar(&s.TlsCert, "etcd-tls-cert", "", "etcd tls cert")
	fs.StringVar(&s.TlsKey, "etcd-tls-key", "", "etcd tls key")
}
