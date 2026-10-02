"""Real pinned LiteLLM against a local fake upstream; never reads .env.

Run explicitly with: python3 -m unittest discover -s services/model-gateway/tests -p test_contract.py -v
"""

import json
import os
import subprocess
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


IMAGE = "hf-model-gateway-contract:task23"


class GatewayContract(unittest.TestCase):
    def check_mode(self, mode):
        requests = []
        scenario = ["text"]
        cancelled = threading.Event()
        arguments = [
            {"rows": [{"x": 1, "flag": False}], "label": " 中\n"},
            {"x": None, "value": 1.25},
        ]
        tool_calls = [
            {
                "type": "function_call",
                "id": f"fc-{index}",
                "call_id": f"call-{index}",
                "name": "lookup",
                "arguments": json.dumps(argument, ensure_ascii=False),
                "status": "completed",
            }
            for index, argument in enumerate(arguments)
        ]
        expected_path = "/v1/chat/completions" if mode == "chat" else "/v1/responses"

        class Upstream(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                requests.append((self.path, self.headers.get("Authorization"), body))
                if self.path != expected_path:
                    self.send_error(404)
                    return
                if scenario[0] == "timeout":
                    time.sleep(33)
                if scenario[0].startswith("http-"):
                    status = int(scenario[0][5:])
                    encoded = json.dumps(
                        {
                            "error": {
                                "message": "HF_SENSITIVE_UPSTREAM_SENTINEL",
                                "type": "upstream_error",
                                "code": "invalid_encrypted_content"
                                if status == 400
                                else str(status),
                            }
                        }
                    ).encode()
                    self.send_response(status)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(encoded)))
                    self.end_headers()
                    self.wfile.write(encoded)
                    return
                current_calls = tool_calls
                json_cases = {
                    "chat-json-truncated": '{"x":1',
                    "chat-json-empty": "",
                    "chat-json-blank": "   ",
                    "chat-json-valid": "{}",
                }
                if scenario[0] in json_cases:
                    current_calls = [
                        {**tool_calls[0], "arguments": json_cases[scenario[0]]}
                    ]
                if scenario[0] in {"tool-id-collision", "stream-tool-id-collision"}:
                    current_calls = [
                        {**item, "call_id": identifier}
                        for item, identifier in zip(tool_calls, ("call.1", "call:1"))
                    ]
                if scenario[0] in {"nan-tool", "stream-nan-tool", "stream-bad-json"}:
                    bad_json = (
                        '{"value":NaN}'
                        if scenario[0] != "stream-bad-json"
                        else '{"broken":'
                    )
                    current_calls = [{**tool_calls[0], "arguments": bad_json}]
                if mode == "chat":
                    response = {
                        "id": "chatcmpl-mock",
                        "object": "chat.completion",
                        "created": 1,
                        "model": "gpt-4.1",
                        "choices": [
                            {
                                "index": 0,
                                "message": {
                                    "role": "assistant",
                                    "content": "  preserved\n",
                                },
                                "finish_reason": "unknown_finish"
                                if scenario[0] == "invalid"
                                else "stop",
                            }
                        ],
                        "usage": {
                            "prompt_tokens": 7,
                            "completion_tokens": 3,
                            "total_tokens": 10,
                        },
                    }
                    if scenario[0] in json_cases or scenario[0] in {
                        "tools",
                        "stream-tools",
                        "truncated",
                        "stream-no-usage",
                        "nan-tool",
                        "stream-nan-tool",
                        "stream-bad-json",
                        "tool-id-collision",
                        "stream-tool-id-collision",
                    }:
                        response["choices"][0].update(
                            {
                                "message": {
                                    "role": "assistant",
                                    "content": None,
                                    "tool_calls": [
                                        {
                                            "id": item["call_id"],
                                            "type": "function",
                                            "function": {
                                                "name": item["name"],
                                                "arguments": item["arguments"],
                                            },
                                        }
                                        for item in current_calls
                                    ],
                                },
                                "finish_reason": "tool_calls",
                            }
                        )
                    if scenario[0] in {"audio", "stream-audio"}:
                        response["choices"][0]["message"]["audio"] = {
                            "id": "aud-synthetic",
                            "data": "YQ==",
                            "transcript": "audio text",
                            "expires_at": 1,
                        }
                else:
                    output = [
                        {
                            "type": "message",
                            "id": "msg-mock",
                            "role": "assistant",
                            "status": "completed",
                            "content": [
                                {
                                    "type": "output_text",
                                    "text": "  preserved\n",
                                    "annotations": [],
                                }
                            ],
                        }
                    ]
                    if scenario[0] == "invalid":
                        output = [
                            {
                                "type": "function_call",
                                "id": "fc-mock",
                                "call_id": "call-mock",
                                "name": "lookup",
                                "arguments": '{"broken":',
                                "status": "completed",
                            }
                        ]
                    if scenario[0] in {
                        "tools",
                        "stream-tools",
                        "truncated",
                        "stream-no-usage",
                        "nan-tool",
                        "stream-nan-tool",
                        "stream-bad-json",
                    }:
                        output = current_calls
                    if scenario[0] in {"reasoning", "stream-reasoning"}:
                        output = [
                            {
                                "type": "reasoning",
                                "id": "rs-mock",
                                "summary": [],
                                "encrypted_content": "opaque-test",
                            }
                        ]
                    if scenario[0] == "reasoning-unencrypted":
                        output = [
                            {"type": "reasoning", "id": "rs-unencrypted", "summary": []}
                        ]
                    if scenario[0] == "unknown-output":
                        output = [{"type": "future_item", "id": "future-mock"}]
                    if scenario[0] in {"unknown-content", "stream-unknown-content"}:
                        output[0]["content"] = [
                            {"type": "output_audio", "data": "synthetic"}
                        ]
                    response = {
                        "id": "resp-mock",
                        "object": "response",
                        "created_at": 1,
                        "model": "gpt-4.1",
                        "status": "completed",
                        "output": output,
                        "parallel_tool_calls": True,
                        "tools": [],
                        "tool_choice": "auto",
                        "usage": {
                            "input_tokens": 7,
                            "output_tokens": 3,
                            "total_tokens": 10,
                        },
                    }
                    if scenario[0].startswith("status-"):
                        response["status"] = scenario[0][7:]
                        response["error"] = (
                            None
                            if response["status"] != "failed"
                            else {
                                "code": "server_error",
                                "message": "HF_SENSITIVE_UPSTREAM_SENTINEL",
                            }
                        )
                    if scenario[0] == "unknown-incomplete":
                        response.update(
                            status="incomplete",
                            incomplete_details={"reason": "future_reason"},
                        )
                if scenario[0] in {"bad-usage", "stream-bad-usage"}:
                    response["usage"][
                        "prompt_tokens" if mode == "chat" else "input_tokens"
                    ] = "7"
                if body.get("stream"):
                    self.send_response(200)
                    self.send_header("Content-Type", "text/event-stream")
                    self.end_headers()

                    def event(item):
                        self.wfile.write(
                            (
                                "data: " + json.dumps(item, ensure_ascii=False) + "\n\n"
                            ).encode()
                        )
                        self.wfile.flush()

                    if scenario[0] == "stream-error":
                        if mode == "chat":
                            event(
                                {
                                    "error": {
                                        "message": "HF_SENSITIVE_UPSTREAM_SENTINEL",
                                        "type": "server_error",
                                        "code": "server_error",
                                    }
                                }
                            )
                        else:
                            event(
                                {
                                    "type": "error",
                                    "message": "HF_SENSITIVE_UPSTREAM_SENTINEL",
                                    "code": "server_error",
                                    "sequence_number": 0,
                                }
                            )
                        return

                    if scenario[0] == "cancel":
                        try:
                            if mode == "responses":
                                event(
                                    {
                                        "type": "response.created",
                                        "sequence_number": 0,
                                        "response": {
                                            **response,
                                            "output": [],
                                            "status": "in_progress",
                                        },
                                    }
                                )
                            for index in range(200):
                                if mode == "chat":
                                    event(
                                        {
                                            "id": "chatcmpl-mock",
                                            "object": "chat.completion.chunk",
                                            "created": 1,
                                            "model": "gpt-4.1",
                                            "choices": [
                                                {
                                                    "index": 0,
                                                    "delta": {
                                                        "content": "still running"
                                                    },
                                                    "finish_reason": None,
                                                }
                                            ],
                                        }
                                    )
                                else:
                                    event(
                                        {
                                            "type": "response.output_text.delta",
                                            "sequence_number": index + 1,
                                            "output_index": 0,
                                            "content_index": 0,
                                            "item_id": "msg-mock",
                                            "delta": "still running",
                                        }
                                    )
                                time.sleep(0.025)
                        except (BrokenPipeError, ConnectionResetError):
                            cancelled.set()
                        return

                    if mode == "chat":

                        def chunk(delta, finish=None, usage=None):
                            value = {
                                "id": "chatcmpl-mock",
                                "object": "chat.completion.chunk",
                                "created": 1,
                                "model": "gpt-4.1",
                                "choices": [
                                    {
                                        "index": 0,
                                        "delta": delta,
                                        "finish_reason": finish,
                                    }
                                ],
                            }
                            if usage is not None:
                                value.update({"usage": usage, "choices": []})
                            event(value)

                        chunk({"role": "assistant", "content": ""})
                        if scenario[0] == "stream-audio":
                            chunk(
                                {
                                    "content": "retained text",
                                    "audio": response["choices"][0]["message"]["audio"],
                                }
                            )
                        for index, item in enumerate(current_calls):
                            encoded = item["arguments"]
                            cut = len(encoded) // 2
                            chunk(
                                {
                                    "tool_calls": [
                                        {
                                            "index": index,
                                            "id": item["call_id"],
                                            "type": "function",
                                            "function": {
                                                "name": item["name"],
                                                "arguments": encoded[:cut],
                                            },
                                        }
                                    ]
                                }
                            )
                            if scenario[0] == "truncated":
                                return
                            chunk(
                                {
                                    "tool_calls": [
                                        {
                                            "index": index,
                                            "function": {"arguments": encoded[cut:]},
                                        }
                                    ]
                                }
                            )
                        chunk({}, "tool_calls")
                        if scenario[0] != "stream-no-usage":
                            chunk({}, usage=response["usage"])
                        self.wfile.write(b"data: [DONE]\n\n")
                    else:
                        event(
                            {
                                "type": "response.created",
                                "sequence_number": 0,
                                "response": {
                                    **response,
                                    "output": [],
                                    "status": "in_progress",
                                },
                            }
                        )
                        if scenario[0] == "stream-reasoning":
                            event(
                                {
                                    "type": "response.output_item.added",
                                    "sequence_number": 1,
                                    "output_index": 0,
                                    "item": response["output"][0],
                                }
                            )
                            event(
                                {
                                    "type": "response.output_item.done",
                                    "sequence_number": 2,
                                    "output_index": 0,
                                    "item": response["output"][0],
                                }
                            )
                        for index, item in enumerate(current_calls):
                            event(
                                {
                                    "type": "response.output_item.added",
                                    "sequence_number": 1 + index * 4,
                                    "output_index": index,
                                    "item": {**item, "arguments": ""},
                                }
                            )
                            encoded = item["arguments"]
                            cut = len(encoded) // 2
                            for number, fragment in enumerate(
                                (encoded[:cut], encoded[cut:])
                            ):
                                event(
                                    {
                                        "type": "response.function_call_arguments.delta",
                                        "sequence_number": 2 + index * 4 + number,
                                        "output_index": index,
                                        "item_id": item["id"],
                                        "delta": fragment,
                                    }
                                )
                                if scenario[0] == "truncated":
                                    return
                            event(
                                {
                                    "type": "response.output_item.done",
                                    "sequence_number": 4 + index * 4,
                                    "output_index": index,
                                    "item": item,
                                }
                            )
                        if scenario[0] == "stream-failed":
                            event(
                                {
                                    "type": "response.failed",
                                    "sequence_number": 10,
                                    "response": {
                                        **response,
                                        "status": "failed",
                                        "error": {
                                            "code": "server_error",
                                            "message": "synthetic",
                                        },
                                    },
                                }
                            )
                        else:
                            if scenario[0] == "stream-no-usage":
                                response.pop("usage")
                            event(
                                {
                                    "type": "response.completed",
                                    "sequence_number": 10,
                                    "response": response,
                                }
                            )
                    return
                if scenario[0] == "no-usage":
                    response.pop("usage")
                encoded = json.dumps(response).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(encoded)))
                self.end_headers()
                try:
                    self.wfile.write(encoded)
                except (BrokenPipeError, ConnectionResetError):
                    pass

        server = ThreadingHTTPServer(("0.0.0.0", 0), Upstream)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        name = f"hf-gateway-contract-{os.getpid()}-{mode}"
        created = False
        try:
            existing = subprocess.run(
                ["docker", "container", "inspect", name], capture_output=True
            )
            self.assertNotEqual(
                existing.returncode, 0, "Refusing an existing test container"
            )
            subprocess.run(
                [
                    "docker",
                    "run",
                    "--detach",
                    "--name",
                    name,
                    "--publish",
                    "127.0.0.1::4000",
                    "--add-host",
                    "host.docker.internal:host-gateway",
                    "--env",
                    "DO_NOT_TRACK=true",
                    "--env",
                    "LITELLM_LOCAL_MODEL_COST_MAP=true",
                    "--env",
                    "HF_GATEWAY_UPSTREAM_MODEL=openai/gpt-4.1",
                    "--env",
                    "OPENAI_API_KEY=fake-openai-key",
                    "--env",
                    "HF_GATEWAY_KEY=sk-fake-gateway-key",
                    "--env",
                    f"OPENAI_BASE_URL=http://host.docker.internal:{server.server_port}/v1",
                    "--env",
                    f"HF_GATEWAY_MODE={mode}",
                    IMAGE,
                    "--config",
                    f"/app/gateway/config-{mode}.yaml",
                    "--port",
                    "4000",
                ],
                check=True,
                capture_output=True,
            )
            created = True
            port = (
                subprocess.check_output(["docker", "port", name, "4000/tcp"], text=True)
                .strip()
                .rsplit(":", 1)[1]
            )
            url = f"http://127.0.0.1:{port}"
            deadline = time.monotonic() + 90
            while True:
                try:
                    with urlopen(url + "/health/liveliness", timeout=2) as response:
                        if response.status == 200:
                            break
                except (URLError, OSError):
                    if time.monotonic() >= deadline:
                        self.fail(
                            "Pinned gateway failed to start (inspect test-container logs locally)"
                        )
                    time.sleep(0.25)

            def call(extra=None, path="/v1/messages", key="sk-fake-gateway-key"):
                payload = {
                    "model": "harness-openai",
                    "max_tokens": 32,
                    "messages": [
                        {"role": "user", "content": "  HF_SENSITIVE_REQUEST_SENTINEL\n"}
                    ],
                }
                payload.update(extra or {})
                request = Request(
                    url + path,
                    json.dumps(payload).encode(),
                    {
                        "Content-Type": "application/json",
                        "x-api-key": key,
                        "anthropic-version": "2023-06-01",
                    },
                )
                try:
                    with urlopen(request, timeout=45) as response:
                        if payload.get("stream"):
                            return response.status, [
                                json.loads(line[6:])
                                for line in response.read().decode().splitlines()
                                if line.startswith("data: ") and line != "data: [DONE]"
                            ]
                        return response.status, json.load(response)
                except HTTPError as error:
                    return error.code, json.load(error)

            status, output = call()
            self.assertEqual(status, 200, output)
            self.assertEqual(
                output["content"], [{"type": "text", "text": "  preserved\n"}]
            )
            self.assertEqual(output["usage"]["input_tokens"], 7)
            self.assertEqual(output["usage"]["output_tokens"], 3)
            self.assertEqual(len(requests), 1)
            self.assertEqual(requests[0][0], expected_path)
            self.assertEqual(requests[0][1], "Bearer fake-openai-key")
            self.assertEqual(requests[0][2]["model"], "gpt-4.1")
            self.assertIs(requests[0][2]["store"], False)
            for extra in (
                {"top_k": 1},
                {"thinking": {"type": "enabled", "budget_tokens": 1024}},
                {"proxy_server_request": {"body": {}}},
                {"metadata": {"user_api_key_user_id": "spoof"}},
                {"user": "spoof"},
                {"callbacks": []},
                {"output_config": {"effort": "high"}},
                {"tool_choice": {"type": "auto", "disable_parallel_tool_use": True}},
                {
                    "tools": [
                        {"name": "tool", "type": "bash_20250124", "input_schema": {}}
                    ]
                },
                {"messages": [{"role": "user", "content": "text", "unknown": True}]},
                {
                    "messages": [
                        {
                            "role": "user",
                            "content": [
                                {"type": "text", "text": "text", "cache_control": {}}
                            ],
                        }
                    ]
                },
                {
                    "messages": [
                        {"role": "user", "content": [{"type": "image", "source": {}}]}
                    ]
                },
                {
                    "messages": [
                        {
                            "role": "user",
                            "content": [{"type": "document", "source": {}}],
                        }
                    ]
                },
                {
                    "messages": [
                        {
                            "role": "assistant",
                            "content": [
                                {
                                    "type": "thinking",
                                    "thinking": "secret",
                                    "signature": "native",
                                }
                            ],
                        }
                    ]
                },
                {
                    "messages": [
                        {
                            "role": "user",
                            "content": [
                                {
                                    "type": "tool_result",
                                    "tool_use_id": "call-1",
                                    "content": "bad",
                                    "is_error": True,
                                }
                            ],
                        }
                    ]
                },
                {
                    "messages": [
                        {
                            "role": "user",
                            "content": [
                                {"type": "text", "text": "before"},
                                {
                                    "type": "tool_result",
                                    "tool_use_id": "call-1",
                                    "content": "ok",
                                },
                            ],
                        }
                    ]
                },
            ):
                with self.subTest(mode=mode, rejected=extra):
                    rejected_status, _ = call(extra)
                    self.assertEqual(rejected_status, 400)
                    self.assertEqual(
                        len(requests), 1, "Reject original body before upstream"
                    )
            for path in (
                "/v1/messages/count_tokens",
                "/v1/chat/completions",
                "/v1/responses",
            ):
                with self.subTest(mode=mode, rejected_path=path):
                    rejected_status, _ = call(path=path)
                    self.assertGreaterEqual(rejected_status, 400)
                    self.assertEqual(len(requests), 1)
            rejected_status, _ = call(key="sk-not-the-gateway-key")
            self.assertEqual(rejected_status, 401)
            self.assertEqual(len(requests), 1)
            scenario[0] = "invalid"
            status, output = call()
            self.assertEqual(len(requests), 2, "No automatic retries")
            self.assertGreaterEqual(
                status,
                400,
                f"{mode} converted invalid upstream semantics into success: {output}",
            )
            schema = {
                "type": "object",
                "properties": {"query": {"type": "array", "items": {"type": "string"}}},
            }
            tools = [{"name": "lookup", "description": "local", "input_schema": schema}]
            scenario[0] = "tools"
            status, output = call({"tools": tools})
            self.assertEqual(status, 200, output)
            expected_tools = [
                {
                    "type": "tool_use",
                    "id": f"call-{index}",
                    "name": "lookup",
                    "input": argument,
                }
                for index, argument in enumerate(arguments)
            ]
            self.assertEqual(output["content"], expected_tools)
            sent_tool = requests[-1][2]["tools"][0]
            sent_tool = sent_tool["function"] if mode == "chat" else sent_tool
            self.assertEqual(sent_tool["parameters"], schema)
            scenario[0] = "text"
            history = [
                {"role": "user", "content": "  first \n"},
                {"role": "assistant", "content": output["content"]},
                {
                    "role": "user",
                    "content": [
                        {
                            "type": "tool_result",
                            "tool_use_id": f"call-{index}",
                            "content": f"  result {index}\n",
                        }
                        for index in range(2)
                    ],
                },
            ]
            status, output = call({"tools": tools, "messages": history})
            self.assertEqual(status, 200, output)
            sent = requests[-1][2]["messages" if mode == "chat" else "input"]
            if mode == "chat":
                self.assertEqual(
                    sent[-2:],
                    [
                        {
                            "role": "tool",
                            "tool_call_id": f"call-{index}",
                            "content": f"  result {index}\n",
                        }
                        for index in range(2)
                    ],
                )
                self.assertEqual(
                    [
                        json.loads(item["function"]["arguments"])
                        for item in sent[1]["tool_calls"]
                    ],
                    arguments,
                )
            else:
                self.assertEqual(
                    sent[-2:],
                    [
                        {
                            "type": "function_call_output",
                            "call_id": f"call-{index}",
                            "output": f"  result {index}\n",
                        }
                        for index in range(2)
                    ],
                )
                self.assertEqual(
                    [
                        json.loads(item["arguments"])
                        for item in sent
                        if item["type"] == "function_call"
                    ],
                    arguments,
                )
            with self.subTest(mode=mode, scenario="system-and-text-blocks"):
                system_texts = ["  system one\n", "\n system two\t"]
                user_texts = ["  user one\n", "\n user two\t"]
                assistant_texts = ["  assistant one\n", "\n assistant two\t"]

                def text_blocks(values):
                    return [{"type": "text", "text": value} for value in values]

                status, output = call(
                    {
                        "system": text_blocks(system_texts),
                        "messages": [
                            {"role": "user", "content": text_blocks(user_texts)},
                            {
                                "role": "assistant",
                                "content": text_blocks(assistant_texts),
                            },
                            {"role": "user", "content": "  next\n"},
                        ],
                    }
                )
                self.assertEqual(status, 200, output)
                sent = requests[-1][2]
                if mode == "chat":
                    self.assertEqual(
                        sent["messages"][0],
                        {"role": "system", "content": text_blocks(system_texts)},
                    )
                    self.assertEqual(
                        sent["messages"][1],
                        {"role": "user", "content": text_blocks(user_texts)},
                    )
                    self.assertEqual(
                        sent["messages"][2]["content"], "".join(assistant_texts)
                    )
                else:
                    # Fixed upstream normalizes system block boundaries with a newline;
                    # every block's text bytes and its relative order stay unchanged.
                    self.assertEqual(sent["instructions"], "\n".join(system_texts))
                    for index, values in enumerate((user_texts, assistant_texts)):
                        self.assertEqual(
                            [
                                block["text"]
                                for block in sent["input"][index]["content"]
                            ],
                            values,
                        )
                self.assertEqual(
                    sent["messages" if mode == "chat" else "input"][-1]["content"],
                    "  next\n"
                    if mode == "chat"
                    else [{"type": "input_text", "text": "  next\n"}],
                )
            scenario[0] = "stream-tools"
            status, events = call({"tools": tools, "stream": True})
            self.assertEqual(status, 200, events)
            partial = {}
            ids = {}
            for event in events:
                if (
                    event["type"] == "content_block_start"
                    and event["content_block"]["type"] == "tool_use"
                ):
                    ids[event["index"]] = event["content_block"]["id"]
                if (
                    event["type"] == "content_block_delta"
                    and event["delta"]["type"] == "input_json_delta"
                ):
                    partial[event["index"]] = (
                        partial.get(event["index"], "") + event["delta"]["partial_json"]
                    )
            self.assertEqual(
                [ids[index] for index in sorted(ids)], ["call-0", "call-1"]
            )
            self.assertEqual(
                [json.loads(partial[index]) for index in sorted(partial)], arguments
            )
            self.assertEqual(events[-1]["type"], "message_stop")
            self.assertEqual(
                [
                    event["usage"]
                    for event in events
                    if event["type"] == "message_delta"
                ][-1],
                {"input_tokens": 7, "output_tokens": 3},
            )
            if mode == "chat":
                for raw_case in (
                    "chat-json-truncated",
                    "chat-json-empty",
                    "chat-json-blank",
                    "tool-id-collision",
                    "audio",
                ):
                    with self.subTest(mode=mode, scenario=raw_case):
                        scenario[0] = raw_case
                        status, output = call({"tools": tools})
                        self.assertGreaterEqual(status, 400, output)
                with self.subTest(mode=mode, scenario="chat-json-valid"):
                    scenario[0] = "chat-json-valid"
                    status, output = call({"tools": tools})
                    self.assertEqual(status, 200, output)
                    self.assertEqual(output["content"][0]["input"], {})
                for raw_case in ("stream-tool-id-collision", "stream-audio"):
                    with self.subTest(mode=mode, scenario=raw_case):
                        scenario[0] = raw_case
                        status, events = call({"tools": tools, "stream": True})
                        self.assertFalse(
                            any(
                                event.get("type") == "message_stop" for event in events
                            ),
                            events,
                        )
            with self.subTest(mode=mode, scenario="nan-tool"):
                scenario[0] = "nan-tool"
                status, output = call({"tools": tools})
                self.assertGreaterEqual(status, 400, output)
            for broken in ("stream-nan-tool", "stream-bad-json"):
                with self.subTest(mode=mode, scenario=broken):
                    scenario[0] = broken
                    status, events = call({"tools": tools, "stream": True})
                    self.assertFalse(
                        any(event.get("type") == "message_stop" for event in events),
                        events,
                    )
                    self.assertTrue(
                        any(event.get("type") == "error" for event in events), events
                    )
            with self.subTest(mode=mode, scenario="no-usage"):
                scenario[0] = "no-usage"
                status, output = call()
                self.assertGreaterEqual(status, 400, output)
            with self.subTest(mode=mode, scenario="bad-usage"):
                scenario[0] = "bad-usage"
                status, output = call()
                self.assertGreaterEqual(status, 400, output)
            with self.subTest(mode=mode, scenario="stream-bad-usage"):
                scenario[0] = "stream-bad-usage"
                status, events = call({"tools": tools, "stream": True})
                self.assertFalse(
                    any(event.get("type") == "message_stop" for event in events), events
                )
            with self.subTest(mode=mode, scenario="stream-no-usage"):
                scenario[0] = "stream-no-usage"
                status, events = call({"tools": tools, "stream": True})
                self.assertFalse(
                    any(event.get("type") == "message_stop" for event in events), events
                )
            for status in (400, 401, 429, 500):
                with self.subTest(mode=mode, upstream_status=status):
                    scenario[0] = f"http-{status}"
                    before = len(requests)
                    returned_status, error_output = call()
                    self.assertEqual(returned_status, status)
                    self.assertEqual(
                        error_output["error"]["message"], "HF_GATEWAY_UPSTREAM_ERROR"
                    )
                    self.assertNotIn(
                        "HF_SENSITIVE_UPSTREAM_SENTINEL", json.dumps(error_output)
                    )
                    self.assertEqual(
                        len(requests), before + 1, "No fallback or strip-and-retry"
                    )
            with self.subTest(mode=mode, scenario="stream-error"):
                scenario[0] = "stream-error"
                status, events = call({"tools": tools, "stream": True})
                self.assertFalse(
                    any(event.get("type") == "message_stop" for event in events), events
                )
                self.assertNotIn("HF_SENSITIVE_UPSTREAM_SENTINEL", json.dumps(events))
            with self.subTest(mode=mode, scenario="truncated"):
                scenario[0] = "truncated"
                status, events = call({"tools": tools, "stream": True})
                self.assertFalse(
                    any(event.get("type") == "message_stop" for event in events), events
                )
            if mode == "responses":
                for unsupported in (
                    "reasoning-unencrypted",
                    "unknown-output",
                    "unknown-incomplete",
                    "unknown-content",
                ):
                    with self.subTest(mode=mode, scenario=unsupported):
                        scenario[0] = unsupported
                        status, output = call()
                        self.assertGreaterEqual(status, 400, output)
                with self.subTest(mode=mode, scenario="stream-unknown-content"):
                    scenario[0] = "stream-unknown-content"
                    status, events = call({"tools": tools, "stream": True})
                    self.assertFalse(
                        any(event.get("type") == "message_stop" for event in events),
                        events,
                    )
                for upstream_state in ("failed", "in_progress", "unknown"):
                    with self.subTest(mode=mode, scenario=upstream_state):
                        scenario[0] = f"status-{upstream_state}"
                        status, output = call()
                        self.assertGreaterEqual(status, 400, output)
                with self.subTest(mode=mode, scenario="reasoning-not-representable"):
                    scenario[0] = "reasoning"
                    status, output = call()
                    self.assertGreaterEqual(status, 400, output)
                with self.subTest(
                    mode=mode, scenario="stream-reasoning-not-representable"
                ):
                    scenario[0] = "stream-reasoning"
                    status, events = call({"tools": tools, "stream": True})
                    self.assertFalse(
                        any(event.get("type") == "message_stop" for event in events),
                        events,
                    )
                with self.subTest(mode=mode, scenario="stream-failed"):
                    scenario[0] = "stream-failed"
                    status, events = call({"tools": tools, "stream": True})
                    self.assertFalse(
                        any(event.get("type") == "message_stop" for event in events),
                        events,
                    )
            with self.subTest(mode=mode, scenario="timeout"):
                scenario[0] = "timeout"
                before = len(requests)
                start = time.monotonic()
                status, output = call()
                self.assertGreaterEqual(status, 400, output)
                self.assertLess(time.monotonic() - start, 40)
                self.assertEqual(len(requests), before + 1, "Timeout must not retry")
            with self.subTest(mode=mode, scenario="cancel"):
                scenario[0] = "cancel"
                before = len(requests)
                request = Request(
                    url + "/v1/messages",
                    json.dumps(
                        {
                            "model": "harness-openai",
                            "max_tokens": 32,
                            "stream": True,
                            "messages": [{"role": "user", "content": "cancel"}],
                        }
                    ).encode(),
                    {
                        "Content-Type": "application/json",
                        "x-api-key": "sk-fake-gateway-key",
                    },
                )
                with urlopen(request, timeout=10) as response:
                    self.assertIn(b"message_start", response.readline())
                self.assertTrue(
                    cancelled.wait(4), "Disconnect must close the upstream stream"
                )
                self.assertEqual(
                    len(requests), before + 1, "Cancellation must not retry"
                )
            if mode == "chat":
                with self.subTest(scenario="late-failure-before-stop"):
                    code = """import asyncio
from callback import guard
async def broken():
    yield b'event: message_stop\\ndata: {"type":"message_stop"}\\n\\n'
    raise ValueError("synthetic late failure")
async def raw_error():
    yield b'event: error\\ndata: {"type":"error","error":{"message":"HF_SENSITIVE_UPSTREAM_SENTINEL"}}\\n\\n'
async def main():
    events=[chunk async for chunk in guard.async_post_call_streaming_iterator_hook(None,broken(),{})]
    assert not any(b"message_stop" in chunk for chunk in events)
    assert any(b"error" in chunk for chunk in events)
    errors=[chunk async for chunk in guard.async_post_call_streaming_iterator_hook(None,raw_error(),{})]
    assert not any(b"HF_SENSITIVE_UPSTREAM_SENTINEL" in chunk for chunk in errors)
asyncio.run(main())"""
                    subprocess.run(
                        ["docker", "exec", name, "python", "-c", code],
                        check=True,
                        capture_output=True,
                    )
            with self.subTest(mode=mode, scenario="safe-logs"):
                logs = subprocess.check_output(
                    ["docker", "logs", name], stderr=subprocess.STDOUT, text=True
                )
                for value in (
                    "HF_SENSITIVE_REQUEST_SENTINEL",
                    "HF_SENSITIVE_UPSTREAM_SENTINEL",
                    "fake-openai-key",
                    "sk-fake-gateway-key",
                ):
                    self.assertTrue(
                        value not in logs,
                        "Gateway logs leaked synthetic sensitive data",
                    )
        finally:
            if created:
                subprocess.run(
                    ["docker", "rm", "--force", name], check=True, capture_output=True
                )
            server.shutdown()
            server.server_close()
            thread.join(timeout=3)

    def test_chat_contract(self):
        self.check_mode("chat")

    def test_responses_contract(self):
        self.check_mode("responses")


if __name__ == "__main__":
    unittest.main()
