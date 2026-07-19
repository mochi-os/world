% MOCHI-WORLD(8) Mochi | Mochi World Server
% Mochisoft OÜ
% 2026

# NAME

mochi-world - realtime game server for the Mochi ecosystem

# SYNOPSIS

**mochi-world** [**-f** *config_file*]

# DESCRIPTION

**mochi-world** is a standalone realtime multiplayer game server: many
simultaneous sessions, each with many players, over WebTransport (QUIC).
It is crash-only — sessions live in memory and nothing durable is stored —
and open: anyone may run one, players choose which server to play on, and
no Mochi server authentication is involved. Durable concerns (identity,
settings, match history, assets) belong to Mochi apps on Mochi servers.

The server provides:

- A lobby HTTP API (default port 4433/tcp) for listing and creating
  matches, server-wide chat, and server status.
- A WebTransport game transport (default port 4433/udp) carrying inputs,
  snapshots, and events at a fixed tick rate.

It runs as the dedicated **mochi** user when installed from the deb/rpm
package, logs to standard output (captured by the journal under systemd),
and needs no state directory except the optional ACME certificate cache.

# OPTIONS

**-f** *config_file*
:   Path to *world.conf*. Defaults to */etc/mochi/world.conf*. A missing
    configuration file is not an error; every setting has a default.

# CONFIGURATION

*world.conf* is an INI file. Every key can also be set by environment
variable as **MOCHI\_<SECTION>\_<KEY>** (for example
**MOCHI_WORLD_NAME**), which takes precedence over the file.

## [log]

**debug**
:   Enable debug logging. Default *false*.

## [world]

**name**
:   The server name shown to players in the lobby. Default *Mochi world*.

**standing**
:   Comma-separated list of permanent sessions to create at startup, in
    addition to each game's default standing session.

## [lobby]

**listen**
:   Lobby bind address. Default all interfaces.

**port**
:   Lobby TCP port. Default *4433*.

## [transport]

**listen**
:   Game transport bind address. Default all interfaces.

**port**
:   Game transport UDP port. Default *4433*.

**address**
:   The public URL advertised to clients, for example
    *https://example.com:4433*. Also used as the subject of the ephemeral
    certificate when no operator certificate is configured.

## [tls]

**certificate**, **key**
:   Paths to an operator-provided certificate chain and private key, serving
    both the lobby and the transport. The pair is reloaded automatically
    when the certificate file changes on disk, so an external renewal
    applies without a restart. Both keys may point at the same file if it
    contains the certificate chain and the key together.

## [acme]

**hosts**
:   Comma-separated hostnames to obtain Let's Encrypt certificates for.
    Enables the built-in ACME client: certificates are obtained and renewed
    automatically. Validation uses HTTP-01, so port 80 must be reachable
    from the internet. Ignored when [tls] certificate is set.

**cache**
:   Directory for obtained certificates. Default */var/lib/mochi-world*.

When neither [tls] nor [acme] is configured, an ephemeral self-signed
certificate is generated and rotated every few days; game clients pin its
hash, which the lobby advertises.

## [limits]

**idle**
:   Seconds before an idle session is removed. Default *300*.

**sessions**
:   Maximum concurrent sessions. Default *100*.

**players**
:   Maximum concurrent players. Default *100*.

**creates**
:   Session creations allowed per client address per minute. Default *10*.

**chats**
:   Lobby chat messages allowed per client address per minute. Default *20*.

# FILES

*/etc/mochi/world.conf*
:   Server configuration.

*/var/lib/mochi-world*
:   ACME certificate cache (only used with [acme]).

# SEE ALSO

**mochi-server**(8)
