package management

import (
	"context"
	"fmt"
	"sync"

	"github.com/choria-io/go-choria/choria"
	choriaconf "github.com/choria-io/go-choria/config"
	"github.com/choria-io/go-choria/server"
	"github.com/choria-io/prometheus-streams/config"
	"github.com/choria-io/prometheus-streams/management/agent"
	"github.com/choria-io/prometheus-streams/management/facts"
	log "github.com/sirupsen/logrus"
)

var (
	fw      *choria.Framework
	cserver *server.Instance
)

// Configure configures the management interface based on the local config
func Configure(conf *config.Config) error {
	cfg, err := choriaconf.NewConfig("/dev/null")
	if err != nil {
		return err
	}

	cfg.Choria.MiddlewareHosts = conf.Management.Brokers
	cfg.Collectives = []string{conf.Management.Collective}
	cfg.MainCollective = cfg.Collectives[0]
	cfg.Choria.UseSRVRecords = false

	if conf.Verbose {
		cfg.LogLevel = "info"
	}

	if conf.Debug {
		cfg.LogLevel = "debug"
	}

	if conf.LogFile != "" {
		cfg.LogFile = conf.LogFile
	}

	cfg.DisableTLS = true

	if conf.Management.TLS != nil {
		cfg.DisableTLS = false

		if conf.Management.TLS.Identity != "" {
			cfg.Identity = conf.Management.TLS.Identity
		}

		switch conf.Management.TLS.Scheme {
		case "puppet":
			cfg.Choria.SecurityProvider = "puppet"
			cfg.Choria.SSLDir = conf.Management.TLS.SSLDir

		case "file", "manual":
			cfg.Choria.SecurityProvider = "file"
			cfg.Choria.FileSecurityCA = conf.Management.TLS.CA
			cfg.Choria.FileSecurityCertificate = conf.Management.TLS.Cert
			cfg.Choria.FileSecurityKey = conf.Management.TLS.Key
			cfg.Choria.FileSecurityCache = conf.Management.TLS.Cache

		default:
			return fmt.Errorf("Security provider '%s' is not supported", conf.Management.TLS.Scheme)
		}
	}

	fw, err = choria.NewWithConfig(cfg)
	if err != nil {
		return err
	}

	facts.Configure(conf)

	cserver, err = server.NewInstance(fw)
	if err != nil {
		return err
	}

	return nil
}

// Run runs the management interface
func Run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	if fw == nil {
		log.Errorf("Cannot configure management, call Configure() first")
		return
	}

	wg.Add(1)
	go facts.Expose(ctx, wg, fw.Config)

	wg.Add(1)
	cserver.Run(ctx, wg)

	err := agent.Register(ctx, fw, cserver)
	if err != nil {
		log.Errorf("Could not register management agent: %s", err)
	}
}
