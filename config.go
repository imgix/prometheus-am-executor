package main

import (
	"flag"
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
)

const (
	defaultListenAddr = ":8080"
)

// Config represents the configuration for this program
type Config struct {
	ListenAddr string     `yaml:"listen_address"`
	Verbose    bool       `yaml:"verbose"`
	TLSKey     string     `yaml:"tls_key"`
	TLSCrt     string     `yaml:"tls_crt"`
	Commands   []*Command `yaml:"commands"`
}

// HasCommand returns true if the config contains the given Command
func (c *Config) HasCommand(other *Command) bool {
	for _, cmd := range c.Commands {
		if cmd.Equal(other) {
			return true
		}
	}
	return false
}

// mergeConfigs returns a config representing all the Configs merged together,
// with later Config structs overriding settings in earlier ones (like ListenAddr).
// Commands are added if they are unique from others.
func mergeConfigs(all ...*Config) *Config {
	var merged = &Config{}

	for _, c := range all {
		if c == nil {
			continue
		}

		if len(c.ListenAddr) > 0 {
			merged.ListenAddr = c.ListenAddr
		}
		merged.Verbose = merged.Verbose || c.Verbose
		if c.TLSKey != "" {
			merged.TLSKey = c.TLSKey
		}
		if c.TLSCrt != "" {
			merged.TLSCrt = c.TLSCrt
		}

		for _, cmd := range c.Commands {
			if !merged.HasCommand(cmd) {
				merged.Commands = append(merged.Commands, cmd)
			}
		}
	}

	return merged
}

// readCli parses cli flags and populates them in a config
// If a yaml config file is also specified, it is also read and merged with the cli config,
// with cli flags taking precedence over settings in the config file.
func readCli() (*Config, error) {
	var cli = &Config{}
	var file *Config
	var err error
	var configFile string
	flag.StringVar(&cli.ListenAddr, "l", "", fmt.Sprintf("HTTP Port to listen on (default \"%s\")", defaultListenAddr))
	flag.BoolVar(&cli.Verbose, "v", false, "Enable verbose/debug logging")
	flag.StringVar(&configFile, "f", "", "YAML config file to use")
	flag.Parse()
	args := flag.Args()

	if len(args) != 0 {
		// Add the command specified at the cli to the config
		cmd := Command{
			Cmd: args[0],
		}

		if len(args) > 1 {
			cmd.Args = args[1:]
		}
		cli.Commands = append(cli.Commands, &cmd)
	}

	if len(configFile) > 0 {
		file, err = readConfigFile(configFile)
		if err != nil {
			return nil, err
		}
	}

	if file != nil {
		// Check that the commands specify resolved_signal values that we can parse
		for i, cmd := range file.Commands {
			_, err := cmd.ParseSignal()
			if err != nil {
				return nil, fmt.Errorf("Invalid resolved_signal specified for command %q at index %d: %w", cmd, i, err)
			}

			if cmd.IgnoreResolved != nil && *cmd.IgnoreResolved {
				log.Printf("Warning: command %q at index %d specifies a resolved_signal, and also specifies to ignore resolved alert. The signal won't be used.", cmd, i)
			}
		}
	}

	return mergeConfigs(file, cli), nil
}

// readConfig reads configuration from supported means (cli flags, config file),
// validates parameters and returns a Config struct.
func readConfig() (*Config, error) {
	c, err := readCli()
	if err != nil {
		flag.Usage()
		return nil, err
	}

	if len(c.Commands) == 0 {
		return nil, fmt.Errorf("missing command to execute on receipt of alarm")
	}

	if len(c.ListenAddr) == 0 {
		c.ListenAddr = defaultListenAddr
	}

	return c, err
}

// readConfigFile reads configuration from a yaml file
func readConfigFile(name string) (*Config, error) {
	var c = &Config{}
	data, err := ioutil.ReadFile(name)
	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal(data, c)
	return c, err
}
