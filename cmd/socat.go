package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"github.com/spf13/cobra"
)

func socatCmd() *cobra.Command {
	return &cobra.Command{
		Use: "socat <addr1> <addr2>",
		Short: "TCP relay between two endpoints",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 { return fmt.Errorf("usage: socat <addr1> <addr2>") }
			return runSocat(cmd.Context(), args[0], args[1])
		},
	}
}

func runSocat(ctx context.Context, spec1, spec2 string) error {
	ln, err := net.Listen("tcp", parseSocatAddr(spec1))
	if err != nil { return fmt.Errorf("socat: listen: %w", err) }
	fmt.Printf("socat: %s -> %s\n", ln.Addr(), parseSocatAddr(spec2))
	for {
		conn, err := ln.Accept()
		if err != nil {
			select { case <-ctx.Done(): return nil; default: }
			continue
		}
		go socatRelay(ctx, conn, parseSocatAddr(spec2))
	}
}

func parseSocatAddr(spec string) string {
	if strings.HasPrefix(spec, "TCP-LISTEN:") {
		return ":" + strings.TrimPrefix(spec, "TCP-LISTEN:")
	}
	if strings.HasPrefix(spec, "TCP:") {
		return strings.TrimPrefix(spec, "TCP:")
	}
	return spec
}

func socatRelay(ctx context.Context, local net.Conn, remote string) {
	defer local.Close()
	rc, err := (&net.Dialer{}).DialContext(ctx, "tcp", remote)
	if err != nil { fmt.Printf("socat: dial %s: %v\n", remote, err); return }
	defer rc.Close()
	done := make(chan error, 2)
	go func() { done <- socatPump(ctx, local, rc) }()
	go func() { done <- socatPump(ctx, rc, local) }()
	<-done
}

func socatPump(ctx context.Context, dst io.Writer, src io.Reader) error {
	buf := make([]byte, 32*1024)
	for {
		select { case <-ctx.Done(): return nil; default: }
		n, err := src.Read(buf)
		if n > 0 { dst.Write(buf[:n]) }
		if err != nil { return err }
	}
}
