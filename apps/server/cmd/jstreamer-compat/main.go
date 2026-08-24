package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jakestreamer/jstreamer-server/internal/compatibility"
)

type usageError struct{}

func (usageError) Error() string {
	return "usage: jstreamer-compat --peer <control|renderer> --peer-fixture <path> --wire-fixture <path> --start-order <old-first|new-first> --server-majors <list>"
}

func main() {
	code, err := runCLI(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		os.Exit(74)
	}
	if code != 0 {
		os.Exit(code)
	}
}

func runCLI(args []string, output, errorOutput io.Writer) (int, error) {
	err := execute(args, output)
	if err == nil {
		return 0, nil
	}
	var protocolError *compatibility.ProtocolError
	if errors.As(err, &protocolError) {
		if writeErr := json.NewEncoder(errorOutput).Encode(protocolError); writeErr != nil {
			return 74, fmt.Errorf("write protocol error: %w", writeErr)
		}
		return 78, nil
	}
	if _, writeErr := fmt.Fprintf(errorOutput, "jstreamer-compat: %v\n", err); writeErr != nil {
		return 74, fmt.Errorf("write command error: %w", writeErr)
	}
	return 65, nil
}

func execute(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("jstreamer-compat", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	kind := flags.String("peer", "", "released peer component: control or renderer")
	peerPath := flags.String("peer-fixture", "", "released peer metadata fixture")
	wirePath := flags.String("wire-fixture", "", "released peer wire request fixture")
	order := flags.String("start-order", "", "deployment order: old-first or new-first")
	serverMajorsValue := flags.String("server-majors", "2,1", "candidate Server protocol majors")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return usageError{}
	}
	serverMajors, err := compatibility.ParseMajorHeader(*serverMajorsValue)
	if err != nil {
		return fmt.Errorf("parse Server majors: %w", err)
	}
	peer, err := os.ReadFile(*peerPath)
	if err != nil {
		return fmt.Errorf("read peer fixture: %w", err)
	}
	wire, err := os.ReadFile(*wirePath)
	if err != nil {
		return fmt.Errorf("read wire fixture: %w", err)
	}
	report, err := compatibility.RunFixture(compatibility.FixtureInput{
		Kind:         compatibility.PeerKind(*kind),
		Order:        compatibility.StartOrder(*order),
		ServerMajors: serverMajors,
		Peer:         peer,
		Wire:         wire,
	})
	if err != nil {
		return err
	}
	if err := json.NewEncoder(output).Encode(report); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}
