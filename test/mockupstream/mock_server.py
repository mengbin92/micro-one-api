#!/usr/bin/env python3
"""Lightweight mock OpenAI-compatible server for E2E testing."""

import json
import time
import uuid
from http.server import HTTPServer, BaseHTTPRequestHandler


class MockHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path == "/chat/completions":
            self._handle_chat_completions()
        else:
            self._respond(404, {"error": "not found"})

    def do_GET(self):
        if self.path == "/health":
            self._respond(200, {"status": "ok"})
        else:
            self._respond(404, {"error": "not found"})

    def _handle_chat_completions(self):
        content_length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(content_length) if content_length > 0 else b""

        try:
            req = json.loads(body) if body else {}
        except json.JSONDecodeError:
            self._respond(400, {"error": "invalid JSON"})
            return

        model = req.get("model", "gpt-3.5-turbo")
        messages = req.get("messages", [])
        prompt_text = messages[-1]["content"] if messages else "unknown"
        response_text = f"Mock response for: {prompt_text}"

        prompt_tokens = max(1, len(prompt_text) // 4)
        completion_tokens = max(1, len(response_text) // 4)

        resp = {
            "id": f"mock-{uuid.uuid4().hex[:12]}",
            "object": "chat.completion",
            "created": int(time.time()),
            "model": model,
            "choices": [
                {
                    "index": 0,
                    "message": {"role": "assistant", "content": response_text},
                    "finish_reason": "stop",
                }
            ],
            "usage": {
                "prompt_tokens": prompt_tokens,
                "completion_tokens": completion_tokens,
                "total_tokens": prompt_tokens + completion_tokens,
            },
        }
        self._respond(200, resp)

    def _respond(self, status, data):
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(data).encode())

    def log_message(self, format, *args):
        pass  # suppress request logs


if __name__ == "__main__":
    port = 9999
    server = HTTPServer(("0.0.0.0", port), MockHandler)
    print(f"Mock upstream listening on :{port}")
    server.serve_forever()
