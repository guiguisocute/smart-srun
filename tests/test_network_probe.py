import os
import struct
import sys
import unittest
from unittest import mock


REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MODULE_ROOT = os.path.join(REPO_ROOT, "root", "usr", "lib", "smart_srun")
THIS_DIR = os.path.dirname(os.path.abspath(__file__))

for path in (THIS_DIR, MODULE_ROOT):
    if path not in sys.path:
        sys.path.insert(0, path)


from _portal_urls import PORTAL_BARE_HOST  # noqa: E402
import network  # noqa: E402  (依赖上方 sys.path 注入，与其余测试文件一致)


class InternetConnectivityProbeTests(unittest.TestCase):
    """连通性探测：裸 socket 单次请求 + 真实 204 状态码判定。

    刻意不用 urllib/http.client（OpenWrt python3-light 缺 unicodedata 时
    idna 编解码器不可用，stdlib 对字符串主机名的 getaddrinfo 直接抛
    LookupError），也不落 wget/uclient-fetch 兜底链（被墙时串行拖满超时）。
    """

    def test_http_204_means_online(self):
        with mock.patch.object(
            network, "_probe_http_status", return_value=204
        ) as probe:
            ok, message = network.test_internet_connectivity(timeout=2)
        self.assertTrue(ok)
        self.assertEqual(message, "")
        # 第一个 URL 就返回 204，不应再探测后续 URL。
        self.assertEqual(probe.call_count, 1)

    def test_redirect_status_means_portal_hijack(self):
        with mock.patch.object(network, "_probe_http_status", return_value=302):
            ok, message = network.test_internet_connectivity(timeout=2)
        self.assertFalse(ok)
        self.assertIn("认证页面", message)

    def test_portal_page_200_means_portal_hijack(self):
        with mock.patch.object(network, "_probe_http_status", return_value=200):
            ok, message = network.test_internet_connectivity(timeout=2)
        self.assertFalse(ok)
        self.assertIn("认证页面", message)

    def test_all_urls_unreachable_reports_failure(self):
        with mock.patch.object(
            network, "_probe_http_status", side_effect=OSError("timed out")
        ) as probe:
            ok, message = network.test_internet_connectivity(timeout=2)
        self.assertFalse(ok)
        self.assertIn("无法访问", message)
        self.assertEqual(probe.call_count, len(network.CONNECTIVITY_CHECK_URLS))

    def test_probe_never_touches_stdlib_http_or_subprocess_fallback(self):
        # 探测既不能依赖 urllib/http.client（python3-light 缺 idna 编解码器时
        # 会 LookupError），也不能落入 http_get 的 wget/uclient-fetch 兜底链
        # （外网被墙时串行拖满多个硬超时，把登录后的终态校验拖死）。
        with mock.patch.object(
            network, "_probe_http_status", side_effect=OSError("unreachable")
        ), mock.patch.object(network, "http_get") as legacy_get, mock.patch.object(
            network, "_http_get_via_stdlib"
        ) as stdlib_get:
            network.test_internet_connectivity(timeout=2)
        legacy_get.assert_not_called()
        stdlib_get.assert_not_called()

    def test_probe_resolves_hostname_as_bytes_to_bypass_idna_codec(self):
        # bytes 主机名走 C 解析器；str 主机名在缺 unicodedata 的设备上会
        # 因 idna 编解码器不可用抛 LookupError('unknown encoding: idna')。
        captured = {}

        def fake_getaddrinfo(host, port, *args, **kwargs):
            captured["host"] = host
            raise OSError("stop before real network IO")

        with mock.patch.object(network.socket, "getaddrinfo", fake_getaddrinfo):
            with self.assertRaises(OSError):
                network._probe_http_status(
                    "http://connect.rom.miui.com/generate_204", timeout=1
                )
        self.assertIsInstance(captured["host"], bytes)

    def test_dns_query_a_parses_compressed_answer(self):
        # 构造带压缩指针的标准 DNS 应答，验证裸 UDP 查询的解析器。
        captured = {}

        class FakeSock:
            def __init__(self, *args, **kwargs):
                pass

            def settimeout(self, timeout):
                pass

            def sendto(self, packet, addr):
                captured["txid"] = packet[:2]
                captured["addr"] = addr

            def recvfrom(self, size):
                resp = captured["txid"] + struct.pack(">HHHHH", 0x8180, 1, 1, 0, 0)
                resp += b"\x01a\x01b\x00" + struct.pack(">HH", 1, 1)  # question
                resp += b"\xc0\x0c" + struct.pack(">HHIH", 1, 1, 60, 4)
                resp += bytes([1, 2, 3, 4])
                return resp, ("10.0.0.53", 53)

            def close(self):
                pass

        with mock.patch.object(network.socket, "socket", FakeSock):
            ips = network._dns_query_a("a.b", "10.0.0.53", timeout=1)

        self.assertEqual(["1.2.3.4"], ips)
        self.assertEqual(("10.0.0.53", 53), captured["addr"])

    def test_resolve_probe_ips_returns_ip_literal_directly(self):
        with mock.patch.object(network, "_uplink_dns_servers") as uplink:
            ips = network._resolve_probe_ips(PORTAL_BARE_HOST, timeout=2)
        self.assertEqual([PORTAL_BARE_HOST], ips)
        uplink.assert_not_called()

    def test_split_http_url_variants(self):
        self.assertEqual(
            ("connect.rom.miui.com", 80, "/generate_204"),
            network._split_http_url("http://connect.rom.miui.com/generate_204"),
        )
        self.assertEqual(
            (PORTAL_BARE_HOST, 8080, "/probe"),
            network._split_http_url("http://%s:8080/probe" % PORTAL_BARE_HOST),
        )
        self.assertEqual(
            ("example.com", 80, "/"),
            network._split_http_url("http://example.com"),
        )


if __name__ == "__main__":
    unittest.main()
