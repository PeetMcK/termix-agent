// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// Check for subcommand
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "enroll":
			runEnroll()
			return
		case "unenroll":
			runUnenroll()
			return
		case "status":
			runStatus()
			return
		case "version", "--version", "-v":
			fmt.Printf("termix-agent %s (commit: %s, built: %s)\n", version, commit, date)
			return
		case "help", "--help", "-h":
			printMainHelp()
			return
		}
	}

	// Default: run agent
	runAgent()
}

func printMainHelp() {
	fmt.Fprintf(os.Stderr, "termix-agent - Termix Terminal Agent\n\n")
	fmt.Fprintf(os.Stderr, "Usage: %s <command> [options]\n\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  enroll     Enroll this agent with a Termix server\n")
	fmt.Fprintf(os.Stderr, "  unenroll   Remove stored credentials and unenroll\n")
	fmt.Fprintf(os.Stderr, "  status     Show enrollment status\n")
	fmt.Fprintf(os.Stderr, "  version    Show version information\n")
	fmt.Fprintf(os.Stderr, "  help       Show this help message\n")
	fmt.Fprintf(os.Stderr, "\nRun '%s <command> --help' for more information on a command.\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\nIf no command is given, the agent will connect using stored credentials.\n")
}

func runEnroll() {
	enrollCmd := flag.NewFlagSet("enroll", flag.ExitOnError)

	server := enrollCmd.String("server", "", "Server address (host:port)")
	token := enrollCmd.String("token", "", "Install token")
	deviceID := enrollCmd.String("id", "", "Device ID (default: hostname)")
	ssl := enrollCmd.Bool("ssl", true, "Use TLS/SSL")
	insecure := enrollCmd.Bool("insecure", false, "Skip TLS verification")
	debug := enrollCmd.Bool("debug", false, "Enable debug logging")

	enrollCmd.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: termix-agent enroll [options]\n\n")
		fmt.Fprintf(os.Stderr, "Enroll this agent with a Termix server.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		enrollCmd.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  termix-agent enroll --server termix.example.com:30007 --token <install-token>\n")
	}

	enrollCmd.Parse(os.Args[2:])

	setupLogging(*debug)

	if *server == "" || *token == "" {
		fmt.Fprintf(os.Stderr, "Error: --server and --token are required\n\n")
		enrollCmd.Usage()
		os.Exit(1)
	}

	cfg := &EnrollConfig{
		Server:   *server,
		Token:    *token,
		DeviceID: *deviceID,
		SSL:      *ssl,
		Insecure: *insecure,
	}

	if err := Enroll(cfg); err != nil {
		log.Fatal().Err(err).Msg("enrollment failed")
	}
}

func runUnenroll() {
	fmt.Println("Removing stored credentials...")
	if err := DeleteCredentials(); err != nil {
		fmt.Printf("Warning: %v\n", err)
		fmt.Println("Credentials may not have been stored.")
	} else {
		fmt.Println("Credentials removed successfully.")
	}
}

func runStatus() {
	creds, err := LoadCredentials()
	if err != nil {
		fmt.Println("Status: Not enrolled")
		fmt.Println("\nRun 'termix-agent enroll' to enroll this agent.")
		return
	}

	fmt.Println("Status: Enrolled")
	fmt.Printf("Server: %s\n", creds.ServerAddr)
	fmt.Printf("Agent ID: %s\n", creds.AgentID)
	fmt.Printf("Device ID: %s\n", creds.DeviceID)
	fmt.Printf("SSL: %v\n", creds.SSL)
	fmt.Println("\nRun 'termix-agent' to connect.")
}

func runAgent() {
	// Check for stored credentials first
	creds, err := LoadCredentials()
	if err != nil {
		fmt.Println("Not enrolled. Please enroll first:")
		fmt.Println("  termix-agent enroll --server <host:port> --token <install-token>")
		fmt.Println("\nRun 'termix-agent help' for more information.")
		os.Exit(1)
	}

	config := DefaultConfig()

	// Use stored credentials as defaults
	config.ServerAddr = creds.ServerAddr
	config.Token = creds.AgentToken
	config.DeviceID = creds.DeviceID
	config.SSL = creds.SSL

	// Allow CLI overrides
	flag.StringVar(&config.ServerAddr, "server", config.ServerAddr, "Server address")
	flag.StringVar(&config.DeviceID, "id", config.DeviceID, "Device ID")
	flag.BoolVar(&config.SSL, "ssl", config.SSL, "Use TLS/SSL")
	flag.BoolVar(&config.Insecure, "insecure", config.Insecure, "Skip TLS verification")
	flag.BoolVar(&config.Reconnect, "reconnect", config.Reconnect, "Auto-reconnect")
	flag.IntVar(&config.Heartbeat, "heartbeat", config.Heartbeat, "Heartbeat interval")
	flag.BoolVar(&config.Debug, "debug", config.Debug, "Enable debug logging")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: termix-agent [options]\n\n")
		fmt.Fprintf(os.Stderr, "Connect to Termix server using stored credentials.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	setupLogging(config.Debug)

	if err := config.Validate(); err != nil {
		log.Fatal().Err(err).Msg("invalid configuration")
	}

	log.Info().
		Str("server", config.ServerAddr).
		Str("deviceId", config.DeviceID).
		Bool("ssl", config.SSL).
		Bool("reconnect", config.Reconnect).
		Msg("starting termix-agent")

	agent := NewAgent(config)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Info().Msg("shutting down...")
		agent.Stop()
	}()

	if err := agent.Run(); err != nil {
		log.Fatal().Err(err).Msg("agent error")
	}

	log.Info().Msg("termix-agent stopped")
}

func setupLogging(debug bool) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	if debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
}
