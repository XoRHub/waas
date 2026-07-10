# waas PulseAudio policy — rendered by waas-entrypoint (@RUNDIR@ is
# substituted), loaded with `pulseaudio -nF`: this file is the COMPLETE
# module list, nothing from the stock default.pa applies.

# No sound hardware in a workspace pod: applications play into a null
# sink and guacd streams that sink's monitor source.
load-module module-null-sink sink_name=waas sink_properties=device.description=waas-audio

# In-session clients (same pod, same user): cookie-authenticated unix
# socket, cookie under $HOME like any PulseAudio user instance.
load-module module-native-protocol-unix socket=@RUNDIR@/pulse/native

# guacd (another pod) pulls the audio stream over TCP. Anonymous auth on
# purpose: the NetworkPolicy that restricts 5901/3389 to guacd is the
# boundary for 4713 too — same threat model as the cleartext VNC/RDP
# traffic (HARDENING.md § Threat model).
load-module module-native-protocol-tcp port=4713 auth-anonymous=1

set-default-sink waas
