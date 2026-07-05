; xrdp template — rendered by waas-entrypoint (@RUNDIR@ substituted).
;
; No sesman, no PAM: the single [vnc-local] backend bridges every RDP
; client onto the local Xvnc session. password=ask forwards the password
; typed by the RDP client (i.e. sent by guacd from the template's
; RDP_PASSWORD) as the VNC password — one shared session secret.
[Globals]
ini_version=1
fork=false
port=3389
address=0.0.0.0
; negotiate lets guacd pick TLS when it can and fall back to standard RDP
; security otherwise; cert/key are runtime-provided (see entrypoint).
security_layer=negotiate
crypt_level=high
certificate=@RUNDIR@/xrdp/cert.pem
key_file=@RUNDIR@/xrdp/key.pem
autorun=vnc-local
allow_channels=true
allow_multimon=false
bitmap_compression=true
max_bpp=24
use_compression=yes

[Logging]
LogFile=/dev/null
LogLevel=INFO
EnableSyslog=false
EnableConsole=true
ConsoleLevel=INFO

[vnc-local]
name=waas-session
lib=libvnc.so
ip=127.0.0.1
port=5901
username=na
password=ask
