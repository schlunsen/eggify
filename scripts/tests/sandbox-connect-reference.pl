#!/usr/bin/perl
# ssh ProxyCommand: tunnels to host:port through the sandbox's HTTP proxy (CONNECT, with its credentials).
use strict; use IO::Socket::INET; use IO::Select; use MIME::Base64;
my ($h, $p) = @ARGV;
my $u = $ENV{HTTPS_PROXY} || $ENV{https_proxy} || $ENV{HTTP_PROXY} || $ENV{http_proxy} or die "no proxy in env\n";
$u =~ m#^https?://(?:([^@/]*)@)?([^:/]+):(\d+)# or die "bad proxy url\n";
my ($auth, $ph, $pp) = ($1, $2, $3);
$auth =~ s/%([0-9A-Fa-f]{2})/chr(hex($1))/ge if defined $auth;
my $s = IO::Socket::INET->new(PeerAddr => $ph, PeerPort => $pp, Proto => 'tcp') or die "proxy connect: $!\n";
my $req = "CONNECT $h:$p HTTP/1.1\r\nHost: $h:$p\r\n";
$req .= "Proxy-Authorization: Basic " . encode_base64($auth, '') . "\r\n" if defined $auth && length $auth;
syswrite($s, "$req\r\n");
my $hdr = '';
while ($hdr !~ /\r\n\r\n/) { my $n = sysread($s, my $b, 1); die "proxy closed\n" unless $n; $hdr .= $b; }
$hdr =~ m#^HTTP/1\.\d 200# or die "proxy refused: " . (split /\r\n/, $hdr)[0] . "\n";
my $sel = IO::Select->new(\*STDIN, $s);
while (1) { for my $fh ($sel->can_read) { my $n = sysread($fh, my $b, 65536); exit 0 unless $n; syswrite($fh == $s ? \*STDOUT : $s, $b); } }
