package setting

import (
	"crypto/tls"
	"fmt"
	"io/fs"
	"os"

	"github.com/spf13/pflag"

	"gopkg.in/ini.v1"
)

type GRPCServerSettings struct {
	Network        string
	Address        string      // with flags, call ProcessAddress to fill this field
	TLSConfig      *tls.Config // with flags, call ProcessTLSConfig to fill this field
	EnableLogging  bool        // log request and response of each unary gRPC call
	MaxRecvMsgSize int
	MaxSendMsgSize int

	// Internal fields
	useTLS   bool
	certFile string
	keyFile  string
}

func gRPCServerSettingsError(msg string, args ...interface{}) error {
	return fmt.Errorf("grpc_server: "+msg, args...)
}

// TODO(gamab) is there a better way to do that with flags
func (c *GRPCServerSettings) ProcessTLSConfig() error {
	if !c.useTLS {
		return nil
	}
	serverCert, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		return gRPCServerSettingsError("error loading X509 key pair: %w", err)
	}
	c.TLSConfig = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.NoClientCert,
	}
	return nil
}

// TODO(gamab) is there a better way to do that with flags
func (c *GRPCServerSettings) ProcessAddress() error {
	switch c.Network {
	case "unix":
		if c.Address != "" {
			// Explicitly provided path for unix domain socket.
			if stat, err := os.Stat(c.Address); os.IsNotExist(err) {
				// File does not exist - nice, nothing to do.
			} else if err != nil {
				return gRPCServerSettingsError("error getting stat for a file: %s", c.Address)
			} else {
				if stat.Mode()&fs.ModeSocket == 0 {
					return gRPCServerSettingsError("file %s already exists and is not a unix domain socket", c.Address)
				}
				// Unix domain socket file, should be safe to remove.
				err := os.Remove(c.Address)
				if err != nil {
					return gRPCServerSettingsError("can't remove unix socket file: %s", c.Address)
				}
			}
		} else {
			// Use temporary file path for a unix domain socket.
			tf, err := os.CreateTemp("", "gf_grpc_server_api")
			if err != nil {
				return gRPCServerSettingsError("error creating tmp file: %v", err)
			}
			unixPath := tf.Name()
			if err := tf.Close(); err != nil {
				return gRPCServerSettingsError("error closing tmp file: %v", err)
			}
			if err := os.Remove(unixPath); err != nil {
				return gRPCServerSettingsError("error removing tmp file: %v", err)
			}
			c.Address = unixPath
		}
		return nil
	case "tcp":
		if c.Address == "" {
			c.Address = "127.0.0.1:10000"
		}
		return nil
	default:
		return gRPCServerSettingsError("unsupported network %s", c.Network)
	}
}

func readGRPCServerSettings(cfg *Cfg, iniFile *ini.File) error {
	server := iniFile.Section("grpc_server")

	cfg.GRPCServer.useTLS = server.Key("use_tls").MustBool(false)
	cfg.GRPCServer.certFile = server.Key("cert_file").String()
	cfg.GRPCServer.keyFile = server.Key("cert_key").String()

	if err := cfg.GRPCServer.ProcessTLSConfig(); err != nil {
		return err
	}

	cfg.GRPCServer.Network = valueAsString(server, "network", "tcp")
	cfg.GRPCServer.Address = valueAsString(server, "address", "")
	cfg.GRPCServer.EnableLogging = server.Key("enable_logging").MustBool(false)
	cfg.GRPCServer.MaxRecvMsgSize = server.Key("max_recv_msg_size").MustInt(0)
	cfg.GRPCServer.MaxSendMsgSize = server.Key("max_send_msg_size").MustInt(0)

	return cfg.GRPCServer.ProcessAddress()
}

func (c *GRPCServerSettings) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&c.Network, "grpc-server-network", "tcp", "Network type for the gRPC server (tcp, unix)")
	fs.StringVar(&c.Address, "grpc-server-address", "", "Address for the gRPC server")
	fs.BoolVar(&c.EnableLogging, "grpc-server-enable-logging", false, "Enable logging of gRPC requests and responses")
	fs.IntVar(&c.MaxRecvMsgSize, "grpc-server-max-recv-msg-size", 0, "Maximum size of a gRPC request message in bytes")
	fs.IntVar(&c.MaxSendMsgSize, "grpc-server-max-send-msg-size", 0, "Maximum size of a gRPC response message in bytes")

	// Internal flags
	fs.BoolVar(&c.useTLS, "grpc-server-use-tls", false, "Enable TLS for the gRPC server")
	fs.StringVar(&c.certFile, "grpc-server-cert-file", "", "Path to the certificate file for the gRPC server")
	fs.StringVar(&c.keyFile, "grpc-server-key-file", "", "Path to the certificate key file for the gRPC server")
}
