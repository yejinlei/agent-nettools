//go:build windows

package tun

import (
	"log"
	"os/exec"
	"strconv"
)

func init() {
	cmd := exec.Command("cmd", "/c", "net session >nul 2>&1")
	if err := cmd.Run(); err != nil {
		log.Println("tun: WARNING - not running as administrator. TUN mode requires admin privileges.")
	}
}

func SetInterfaceMTU(iface string, mtu int) error {
	cmd := exec.Command("cmd", "/c",
		"netsh interface ipv4 set subinterface \""+iface+"\" mtu="+strconv.Itoa(mtu)+" store=persistent")
	return cmd.Run()
}