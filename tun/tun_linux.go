//go:build !windows

package tun

import (
	"os/exec"
	"strconv"
)

func SetInterfaceMTU(iface string, mtu int) error {
	return exec.Command("ip", "link", "set", "dev", iface, "mtu", strconv.Itoa(mtu)).Run()
}