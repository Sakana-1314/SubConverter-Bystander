#!/usr/bin/env python3
"""冒烟测试用的假上游：/provider 返回流量头，/sub 充当转换器。"""
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

USERINFO = "upload=1048576; download=5242880; total=107374182400; expire=1900000000"


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.startswith("/provider"):
            body = b"proxies: []\n"
            self.send_response(200)
            self.send_header("Subscription-Userinfo", USERINFO)
        elif self.path.startswith("/sub"):
            body = b"proxy-providers:\n  demo:\n    type: http\n"
            self.send_response(200)
            self.send_header("profile-update-interval", "24")
        else:
            self.send_response(404)
            self.end_headers()
            return
        self.send_header("Content-Type", "text/plain; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    do_HEAD = do_GET

    def log_message(self, *args):
        pass


ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
