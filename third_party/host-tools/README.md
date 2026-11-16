# Recovery host tools

These optional recovery scripts invoke host-installed programs; their binaries are
not embedded or redistributed by Pastime. The synthetic recovery rehearsal used
GnuPG **2.4.7** (GPL-3.0-or-later), reporting libgcrypt **1.11.0**. The installed
Debian GnuPG copyright inventory and GPL-3 text are retained here from
`/usr/share/doc/gnupg/copyright` and `/usr/share/common-licenses/GPL-3` respectively.
They include the component exceptions rather than assuming every bundled source
file has one license. GnuPG's declared source is https://gnupg.org/download/.

Record the target recovery host's exact package/SBOM inventory before appliance
redistribution. Host Docker, OpenSSH `scp`, tar and coreutils remain external
operating-system tools, as with the existing installation scripts. No provider
SDK, copied reference-application source or product asset was introduced.

See the [recovery procedure](../../deploy/lxc/RECOVERY.md). The checker verifies a secret written through the application; wrong application keys fail verification.
