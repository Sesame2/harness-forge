"""Official LiteLLM hooks: authenticate and validate before any conversion."""

import hmac
import json
import os

from fastapi import HTTPException, Request
from litellm.integrations.custom_logger import CustomLogger
from litellm.proxy._types import UserAPIKeyAuth

from capabilities import validate_request


async def authenticate(request: Request, api_key: str) -> UserAPIKeyAuth:
    expected = os.environ.get("HF_GATEWAY_KEY", "")
    if not expected or not api_key or not hmac.compare_digest(api_key, expected):
        raise HTTPException(401, "HF_GATEWAY_UNAUTHORIZED")
    if request.url.path == "/v1/messages/count_tokens":
        raise HTTPException(501, "HF_GATEWAY_TOKEN_COUNT_UNAVAILABLE")
    if request.method != "POST" or request.url.path != "/v1/messages":
        raise HTTPException(403, "HF_GATEWAY_ROUTE_UNSUPPORTED")
    try:
        # Request.json() is the original transport body, not LiteLLM's cleaned
        # proxy_server_request snapshot or client-supplied metadata.
        validate_request(await request.json(), os.environ.get("HF_GATEWAY_MODE", ""))
    except (ValueError, TypeError, json.JSONDecodeError) as error:
        code = (
            str(error)
            if str(error).startswith("HF_GATEWAY_")
            else "HF_GATEWAY_INVALID_JSON"
        )
        raise HTTPException(400, code) from None
    return UserAPIKeyAuth(api_key=api_key)


class CapabilityGuard(CustomLogger):
    async def async_pre_call_hook(self, user_api_key_dict, cache, data, call_type):
        if call_type != "anthropic_messages":
            raise HTTPException(403, "HF_GATEWAY_ROUTE_UNSUPPORTED")
        return data

    async def async_post_call_failure_hook(
        self, request_data, original_exception, user_api_key_dict, traceback_str=None
    ):
        status = getattr(original_exception, "status_code", 502)
        if type(status) is not int or not 400 <= status <= 599:
            status = 502
        return HTTPException(status, "HF_GATEWAY_UPSTREAM_ERROR")

    async def async_post_call_success_hook(self, data, user_api_key_dict, response):
        for block in response.get("content", []):
            if block.get("type") not in {"text", "tool_use"}:
                raise HTTPException(502, "HF_GATEWAY_OUTPUT_UNSUPPORTED")
            if block.get("type") == "tool_use":
                try:
                    json.dumps(block["input"], allow_nan=False)
                except (ValueError, TypeError):
                    raise HTTPException(
                        502, "HF_GATEWAY_INVALID_TOOL_ARGUMENTS"
                    ) from None
        return response

    async def async_post_call_streaming_iterator_hook(
        self, user_api_key_dict, response, request_data
    ):
        try:
            async for chunk in self._validated_stream(response):
                yield chunk
        except Exception as error:
            code = getattr(error, "detail", "HF_GATEWAY_UPSTREAM_ERROR")
            if not isinstance(code, str) or not code.startswith("HF_GATEWAY_"):
                code = "HF_GATEWAY_UPSTREAM_ERROR"
            yield (
                "event: error\ndata: "
                + json.dumps(
                    {"type": "error", "error": {"type": "api_error", "message": code}}
                )
                + "\n\n"
            ).encode()

    async def _validated_stream(self, response):
        arguments = {}
        stopped = False
        terminal = None
        async for chunk in response:
            if stopped:
                raise HTTPException(502, "HF_GATEWAY_INVALID_STREAM_ORDER")
            # The official Anthropic adapter yields one complete SSE event per
            # chunk. Validate it without changing a byte or buffering the stream.
            text = chunk.decode() if isinstance(chunk, bytes) else chunk
            if not isinstance(text, str):
                raise HTTPException(502, "HF_GATEWAY_STREAM_UNSUPPORTED")
            for line in text.splitlines():
                if not line.startswith("data: "):
                    continue
                event = json.loads(line[6:])
                kind = event.get("type")
                if kind == "error":
                    raise HTTPException(502, "HF_GATEWAY_UPSTREAM_ERROR")
                elif kind == "content_block_start":
                    block = event.get("content_block", {})
                    if block.get("type") not in {"text", "tool_use"}:
                        raise HTTPException(502, "HF_GATEWAY_OUTPUT_UNSUPPORTED")
                    if block.get("type") == "tool_use":
                        arguments[event["index"]] = ""
                elif (
                    kind == "content_block_delta"
                    and event.get("delta", {}).get("type") == "input_json_delta"
                ):
                    index = event["index"]
                    if index not in arguments:
                        raise HTTPException(502, "HF_GATEWAY_INVALID_TOOL_ARGUMENTS")
                    arguments[index] += event["delta"]["partial_json"]
                    # ponytail: cap each in-flight tool argument at 1 MiB; use a
                    # streaming JSON validator only if larger tool inputs matter.
                    if len(arguments[index].encode()) > 1024 * 1024:
                        raise HTTPException(502, "HF_GATEWAY_TOOL_ARGUMENTS_TOO_LARGE")
                elif kind == "content_block_stop" and event["index"] in arguments:
                    try:
                        value = json.loads(arguments.pop(event["index"]))
                        if not isinstance(value, dict):
                            raise ValueError
                        json.dumps(value, allow_nan=False)
                    except (ValueError, TypeError):
                        raise HTTPException(
                            502, "HF_GATEWAY_INVALID_TOOL_ARGUMENTS"
                        ) from None
                elif kind == "message_stop":
                    if arguments:
                        raise HTTPException(502, "HF_GATEWAY_INVALID_TOOL_ARGUMENTS")
                    stopped = True
                    terminal = chunk
            if not stopped:
                yield chunk
        if not stopped:
            raise HTTPException(502, "HF_GATEWAY_TRUNCATED_STREAM")
        yield terminal


guard = CapabilityGuard()
