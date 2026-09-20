#!/usr/bin/env python3
"""Development-host TCP/UDP echo measurements across a manually initiated switch.

Server: python3 scripts/measure_switch_continuity.py serve --listen 127.0.0.1
Client: python3 scripts/measure_switch_continuity.py measure --host 127.0.0.1

Run the client on a LAN game-client equivalent and the server on a controlled
reachable host. This tool never changes routes, interfaces, or authentication.
It opens exactly one TCP connection (no reconnection hides a broken session).
Both protocols report observed client address/port, RTT, loss and reply gaps.
Results describe this echo experiment, not compatibility with any game protocol.
Python belongs on development hosts, never in the OpenWrt package.
"""

import argparse
import ipaddress
import json
import secrets
import selectors
import socket
import socketserver
import threading
import time


def reply(data, peer):
    if len(data) > 512:
        return None
    try:
        value = json.loads(data)
        seq, nonce = value["seq"], value["nonce"]
        if type(seq) is not int or not 0 <= seq < 1000000000:
            return None
        if not isinstance(nonce, str) or len(nonce) != 32 or any(c not in "0123456789abcdef" for c in nonce):
            return None
        return (json.dumps(dict(seq=seq, nonce=nonce, peer=list(peer[:2])), separators=(",", ":")) + "\n").encode()
    except (ValueError, KeyError, TypeError):
        return None


class TCPHandler(socketserver.StreamRequestHandler):
    def handle(self):
        self.request.settimeout(120)
        try:
            while True:
                line = self.rfile.readline(513)
                if not line:
                    return
                output = reply(line, self.client_address)
                if output is None:
                    return
                self.wfile.write(output)
                self.wfile.flush()
        except OSError:
            return


class UDPHandler(socketserver.BaseRequestHandler):
    def handle(self):
        data, connection = self.request
        output = reply(data, self.client_address)
        if output:
            connection.sendto(output, self.client_address)


class TCPServer(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


class EchoServer:
    def __init__(self, host, port):
        self.tcp = TCPServer((host, port), TCPHandler)
        try:
            self.udp = socketserver.UDPServer(self.tcp.server_address, UDPHandler)
        except Exception:
            self.tcp.server_close()
            raise
        self.port = self.tcp.server_address[1]

    def __enter__(self):
        for server in (self.tcp, self.udp):
            threading.Thread(target=server.serve_forever, kwargs=dict(poll_interval=.05), daemon=True).start()
        return self

    def __exit__(self, *_):
        for server in (self.tcp, self.udp):
            server.shutdown()
            server.server_close()


class Samples:
    def __init__(self, started):
        self.started = self.last = started
        self.sent = {}
        self.received = set()
        self.endpoints = []
        self.highest = -1
        self.reordered = self.duplicates = self.errors = 0
        self.max_gap = self.max_rtt = 0

    def accept(self, data, nonce, now):
        try:
            value = json.loads(data)
            seq, peer = value["seq"], value["peer"]
            if value["nonce"] != nonce or type(seq) is not int or seq not in self.sent:
                return False
            if not isinstance(peer, list) or len(peer) != 2 or type(peer[1]) is not int:
                return False
            ipaddress.IPv4Address(peer[0])
            if seq in self.received:
                self.duplicates += 1
                return False
            self.reordered += int(seq < self.highest)
            self.highest = max(seq, self.highest)
            self.received.add(seq)
            self.max_gap = max(self.max_gap, now - self.last)
            self.last = now
            self.max_rtt = max(self.max_rtt, now - self.sent[seq])
            if peer not in self.endpoints and len(self.endpoints) < 16:
                self.endpoints.append(peer)
            return True
        except (ValueError, KeyError, TypeError):
            return False

    def result(self, ended):
        return dict(requests_started=len(self.sent), replies=len(self.received),
                    unanswered=len(self.sent) - len(self.received),
                    max_reply_gap_ms=round(1000 * max(self.max_gap, ended - self.last), 3),
                    last_reply_age_ms=round(1000 * (ended - self.last), 3),
                    max_rtt_ms=round(1000 * self.max_rtt, 3),
                    reordered=self.reordered, duplicate_replies=self.duplicates,
                    socket_errors=self.errors, observed_client_endpoints=self.endpoints)


def measure(host, port, duration=30, interval=.05):
    ipaddress.IPv4Address(host)  # Pin one endpoint; DNS changes are not a switch measurement.
    if not 0 < duration <= 3600 or not .02 <= interval <= 5:
        raise ValueError("duration must be (0, 3600], interval [0.02, 5]")
    tcp = socket.create_connection((host, port), timeout=3)
    tcp.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
    udp = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    selector = selectors.DefaultSelector()
    try:
        udp.connect((host, port))
        for name, connection in (("tcp", tcp), ("udp", udp)):
            connection.setblocking(False)
            selector.register(connection, selectors.EVENT_READ, name)
        started = time.monotonic()
        deadline = started + duration
        stats = {name: Samples(started) for name in ("tcp", "udp")}
        nonce = secrets.token_hex(16)
        next_send, sequence = started, 0
        pending, incoming = b"", b""
        tcp_waiting, broken = False, False
        while time.monotonic() < deadline:
            now = time.monotonic()
            if now >= next_send:
                data = (json.dumps(dict(seq=sequence, nonce=nonce), separators=(",", ":")) + "\n").encode()
                stats["udp"].sent[sequence] = now
                try:
                    udp.send(data)
                except OSError:
                    stats["udp"].errors += 1
                if not broken and not tcp_waiting:
                    pending = data
                    stats["tcp"].sent[sequence] = now
                    tcp_waiting = True
                    selector.modify(tcp, selectors.EVENT_READ | selectors.EVENT_WRITE, "tcp")
                sequence += 1
                next_send = now + interval
            for key, events in selector.select(max(0, min(next_send, deadline) - time.monotonic())):
                name, connection = key.data, key.fileobj
                try:
                    if name == "tcp" and events & selectors.EVENT_WRITE:
                        pending = pending[connection.send(pending):]
                        if not pending:
                            selector.modify(tcp, selectors.EVENT_READ, "tcp")
                    if events & selectors.EVENT_READ:
                        data = connection.recv(4096)
                        if name == "tcp":
                            if not data:
                                raise ConnectionResetError("peer closed TCP")
                            incoming += data
                            if len(incoming) > 4096:
                                raise OSError("oversized TCP response")
                            while b"\n" in incoming:
                                line, incoming = incoming.split(b"\n", 1)
                                if stats[name].accept(line, nonce, time.monotonic()):
                                    tcp_waiting = False
                        else:
                            stats[name].accept(data, nonce, time.monotonic())
                except BlockingIOError:
                    pass
                except OSError:
                    stats[name].errors += 1
                    if name == "tcp":
                        broken = True
                        selector.unregister(tcp)
                        tcp.close()
        ended = time.monotonic()
        results = {name: sample.result(ended) for name, sample in stats.items()}
        results["tcp"]["connection_broken"] = broken
        results["tcp"]["connection_attempts"] = 1
        results["tcp"]["same_connection_replied_at_end"] = bool(
            not broken and stats["tcp"].received and ended - stats["tcp"].last < max(.5, 5 * interval))
        return dict(schema_version=1, duration_s=round(ended - started, 3), interval_s=interval,
                    server=[host, port], started_unix=time.time() - (ended - started),
                    traffic=results, note="Reply gaps include start/end silence; no route or authentication changes are made.")
    finally:
        selector.close()
        tcp.close()
        udp.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    server = commands.add_parser("serve")
    server.add_argument("--listen", default="127.0.0.1", type=str)
    server.add_argument("--port", type=int, default=18946)
    client = commands.add_parser("measure")
    client.add_argument("--host", required=True)
    client.add_argument("--port", type=int, default=18946)
    client.add_argument("--duration", type=float, default=30)
    client.add_argument("--interval", type=float, default=.05)
    args = parser.parse_args()
    if args.command == "serve":
        ipaddress.IPv4Address(args.listen)
        with EchoServer(args.listen, args.port) as server:
            print(json.dumps(dict(listen=args.listen, port=server.port)), flush=True)
            try:
                threading.Event().wait()
            except KeyboardInterrupt:
                pass
    else:
        print(json.dumps(measure(args.host, args.port, args.duration, args.interval), indent=2))


if __name__ == "__main__":
    main()
