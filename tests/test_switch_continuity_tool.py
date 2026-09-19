"""The dev probe must distinguish a broken TCP session from UDP reachability."""
import importlib.util
from pathlib import Path

PATH = Path(__file__).resolve().parents[1] / "scripts" / "measure_switch_continuity.py"
SPEC = importlib.util.spec_from_file_location("continuity_tool", PATH)
tool = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(tool)


def test_persistent_tcp_and_udp_baseline():
    with tool.EchoServer("127.0.0.1", 0) as server:
        result = tool.measure("127.0.0.1", server.port, duration=.25, interval=.02)
    tcp, udp = result["traffic"]["tcp"], result["traffic"]["udp"]
    assert tcp["replies"] >= 2 and udp["replies"] >= 2
    assert tcp["connection_attempts"] == 1 and not tcp["connection_broken"]
    assert tcp["same_connection_replied_at_end"]
    assert len(tcp["observed_client_endpoints"]) == len(udp["observed_client_endpoints"]) == 1


def test_tcp_disconnect_is_not_hidden_by_reconnect(monkeypatch):
    def close_after_one(self):
        line = self.rfile.readline(513)
        self.wfile.write(tool.reply(line, self.client_address))
        self.wfile.flush()

    monkeypatch.setattr(tool.TCPHandler, "handle", close_after_one)
    with tool.EchoServer("127.0.0.1", 0) as server:
        result = tool.measure("127.0.0.1", server.port, duration=.25, interval=.02)
    tcp, udp = result["traffic"]["tcp"], result["traffic"]["udp"]
    assert tcp["connection_broken"] and tcp["connection_attempts"] == 1
    assert not tcp["same_connection_replied_at_end"]
    assert tcp["replies"] == 1 and udp["replies"] >= 2
    assert tcp["max_reply_gap_ms"] > udp["max_reply_gap_ms"]


def test_loss_reorder_duplicates_and_endpoint_change_are_reported():
    sample = tool.Samples(0)
    sample.sent = {0: .0, 1: .1, 2: .2, 3: .3}
    nonce = "a" * 32
    def receive(seq, at, port):
        return sample.accept(tool.reply(tool.json.dumps(dict(seq=seq, nonce=nonce)).encode(), ("192.0.2.2", port)), nonce, at)
    assert receive(1, .4, 100)
    assert receive(0, .5, 100)
    assert not receive(0, .6, 100)
    assert receive(3, 1.5, 200)
    result = sample.result(2)
    assert result["unanswered"] == 1
    assert result["reordered"] == result["duplicate_replies"] == 1
    assert result["max_reply_gap_ms"] == 1000
    assert result["last_reply_age_ms"] == 500
    assert result["observed_client_endpoints"] == [["192.0.2.2", 100], ["192.0.2.2", 200]]
