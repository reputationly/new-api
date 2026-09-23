"""端到端测试用的假上游：OpenAI 兼容，返回固定用量，扣费可以精确手算。

- POST /v1/chat/completions：usage 恒为 prompt 1000 / completion 500；
  模型名含 "fail" 时返回 500（用来测失败退款）。
- POST /v1/images/generations：返回一张 1x1 png（按次计费模型）。
- GET  /v1/models：列出请求里出现过的模型，供渠道测试用。

只绑 127.0.0.1，不出网。
"""
import json
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PNG_1X1 = (
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
)


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):  # 安静
        pass

    def _json(self, code, body):
        data = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def _body(self):
        n = int(self.headers.get("Content-Length") or 0)
        return json.loads(self.rfile.read(n) or b"{}")

    def do_GET(self):
        if self.path.startswith("/v1/models"):
            return self._json(200, {"object": "list", "data": []})
        self._json(404, {"error": "not found"})

    def do_POST(self):
        body = self._body()
        model = body.get("model", "")
        if "fail" in model:
            return self._json(500, {"error": {"message": "mock upstream failure", "type": "server_error"}})
        if self.path.startswith("/v1/chat/completions"):
            return self._json(200, {
                "id": "chatcmpl-mock",
                "object": "chat.completion",
                "created": int(time.time()),
                "model": model,
                "choices": [{"index": 0, "message": {"role": "assistant", "content": "ok"}, "finish_reason": "stop"}],
                "usage": {"prompt_tokens": 1000, "completion_tokens": 500, "total_tokens": 1500},
            })
        if self.path.startswith("/v1/images/generations"):
            return self._json(200, {"created": int(time.time()), "data": [{"b64_json": PNG_1X1}]})
        self._json(404, {"error": "not found"})


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18080
    ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
