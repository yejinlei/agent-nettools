package tun

import (
	"fmt"
	"log"
	"net"
	"os/exec"
	"runtime"
)

func AddRoute(dest, gateway string, ifaceName string) error {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("cmd", "/c",
			fmt.Sprintf("route add %s %s IF 1", dest, gateway))
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("tun: add route %s -> %s: %s", dest, gateway, string(output))
			return err
		}
		return nil
	}
	cmd := exec.Command("ip", "route", "add", dest, "via", gateway, "dev", ifaceName)
	return cmd.Run()
}

func DelRoute(dest, gateway string, ifaceName string) error {
	if runtime.GOOS == "windows" {
		exec.Command("cmd", "/c", fmt.Sprintf("route delete %s", dest)).Run()
		return nil
	}
	exec.Command("ip", "route", "del", dest, "via", gateway, "dev", ifaceName).Run()
	return nil
}

func GetDefaultGateway() (net.IP, error) {
	if runtime.GOOS == "windows" {
		output, err := exec.Command("cmd", "/c", "route print 0.0.0.0").Output()
		if err != nil {
			return nil, fmt.Errorf("route print: %w", err)
		}
		_ = string(output)
	}
	return net.ParseIP("0.0.0.0"), nil
}

