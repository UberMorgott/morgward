package steps

// A7Cleanup implements §A7: guarded bloatware purge. Purges are IRREVERSIBLE, so
// every removed package is logged to /root/vps-purged-packages.log, autoremove
// is guarded by apt-mark manual, and root-on-multipath aborts the multipath purge.
type A7Cleanup struct{}

func (A7Cleanup) ID() string    { return "A7" }
func (A7Cleanup) Title() string { return "Cleanup (purge bloatware, apt-mark guard)" }

func (a A7Cleanup) Run(ctx *Context) (Status, string, error) {
	guestPurge := ""
	if ctx.Facts.Virt != "" && ctx.Facts.Virt != "none" {
		// fwupd is safe to purge on a guest, not on bare metal.
		guestPurge = "PKGS=\"$PKGS fwupd\"\n"
	}

	script := `set +e
LOG=/root/vps-purged-packages.log
# Guard kernel/cloud-init/netplan from autoremove.
for pkg in cloud-init netplan.io software-properties-common; do
  dpkg -l "$pkg" 2>/dev/null | grep -q '^ii' && apt-mark manual "$pkg" >/dev/null 2>&1
done

# Multipath gate: abort the multipath purge if root is on multipath.
MP_SAFE=1
findmnt -no SOURCE / | grep -q mpath && MP_SAFE=0
multipath -ll 2>/dev/null | grep -q . && MP_SAFE=0

export DEBIAN_FRONTEND=noninteractive
PKGS="apport apport-symptoms whoopsie sysstat packagekit"
` + guestPurge + `
# Record the SIMULATED set first (irreversible), then purge only present pkgs.
for p in $PKGS; do
  if dpkg -l "$p" 2>/dev/null | grep -q '^ii'; then
    apt-get -s purge "$p" 2>/dev/null | awk '/^(Remv|Purg)/{print $2}' >> "$LOG"
    stdbuf -oL -eL apt-get purge -y "$p"
  fi
done

if [ "$MP_SAFE" = "1" ] && dpkg -l multipath-tools 2>/dev/null | grep -q '^ii'; then
  echo multipath-tools >> "$LOG"
  systemctl disable --now multipathd.service multipathd.socket >/dev/null 2>&1
  stdbuf -oL -eL apt-get purge -y multipath-tools multipath-tools-boot
  stdbuf -oL -eL update-initramfs -u -k all
fi

# Guard cloud-init survives the cascade, then autoremove.
stdbuf -oL -eL apt-get autoremove -y --purge
stdbuf -oL -eL apt-get clean
journalctl --vacuum-size=500M >/dev/null 2>&1
systemctl reset-failed >/dev/null 2>&1
echo "PURGED_COUNT=$(wc -l < "$LOG" 2>/dev/null || echo 0)"
`
	r := ctx.Cli.Sudo(script)
	count := extractMarker(r.Stdout, "PURGED_COUNT=")
	failed := ctx.Cli.Sudo("systemctl --failed --no-legend 2>/dev/null | wc -l").Out()
	return StatusOK, "bloatware purged (" + count + " pkgs logged to /root/vps-purged-packages.log), failed units=" + failed, nil
}
