"""Fail-closed Anthropic subset accepted by the pinned OpenAI adapters."""

import json
import re


def _require(condition, code="UNSUPPORTED_REQUEST"):
    if not condition:
        raise ValueError("HF_GATEWAY_" + code)


def _object(value, allowed, required=()):
    _require(
        isinstance(value, dict)
        and set(value) <= set(allowed)
        and set(required) <= set(value)
    )


def _text_blocks(value):
    if isinstance(value, str):
        return
    _require(isinstance(value, list) and bool(value))
    for block in value:
        _object(block, {"type", "text"}, {"type", "text"})
        _require(block["type"] == "text" and isinstance(block["text"], str))


def validate_usage(usage, mode):
    """Check provider counts before LiteLLM can coerce, estimate, or fill them."""
    if hasattr(usage, "model_dump"):
        usage = usage.model_dump()
    names = (
        ("prompt_tokens", "completion_tokens")
        if mode == "chat"
        else ("input_tokens", "output_tokens")
    )
    _require(isinstance(usage, dict), "USAGE_UNAVAILABLE")
    _require(
        all(type(usage.get(name)) is int and usage[name] >= 0 for name in names),
        "INVALID_USAGE",
    )
    if "total_tokens" in usage:
        _require(
            type(usage["total_tokens"]) is int and usage["total_tokens"] >= 0,
            "INVALID_USAGE",
        )
    for details in (
        "prompt_tokens_details",
        "completion_tokens_details",
        "input_tokens_details",
        "output_tokens_details",
    ):
        if usage.get(details) is not None:
            _require(isinstance(usage[details], dict), "INVALID_USAGE")
            _require(
                all(
                    value is None or (type(value) is int and value >= 0)
                    for value in usage[details].values()
                ),
                "INVALID_USAGE",
            )


def validate_chat_output(body, stream):
    """Check raw Chat JSON before the SDK or adapter can repair or discard it."""
    for choice in body.get("choices", []):
        message = choice.get("delta" if stream else "message") or {}
        _require(
            message.get("content") is None or isinstance(message["content"], str),
            "OUTPUT_UNSUPPORTED",
        )
        for field in (
            "audio",
            "annotations",
            "function_call",
            "reasoning",
            "reasoning_content",
            "reasoning_details",
            "thinking_blocks",
            "provider_specific_fields",
        ):
            _require(not message.get(field), "OUTPUT_UNSUPPORTED")
        for call in message.get("tool_calls") or []:
            _require(
                call.get("type") == "function" or (stream and call.get("type") is None),
                "OUTPUT_UNSUPPORTED",
            )
            if not stream or call.get("id") is not None:
                identifier = call.get("id")
                # The pinned helper has no length cap; it replaces invalid chars
                # and strips __thought__ suffixes. Accept only unchanged IDs.
                _require(
                    isinstance(identifier, str)
                    and re.fullmatch(r"[A-Za-z0-9_-]+", identifier) is not None
                    and "__thought__" not in identifier,
                    "TOOL_ID_UNREPRESENTABLE",
                )
            function = call.get("function") or {}
            _require(
                not call.get("provider_specific_fields")
                and not function.get("provider_specific_fields"),
                "OUTPUT_UNSUPPORTED",
            )
            if not stream:
                try:
                    arguments = json.loads(function.get("arguments"))
                    if not isinstance(arguments, dict):
                        raise ValueError
                    json.dumps(arguments, allow_nan=False)
                except (ValueError, TypeError):
                    _require(False, "INVALID_TOOL_ARGUMENTS")


def validate_response_status(body):
    """Responses failures and unknown incomplete reasons are not successful turns."""
    status = body.get("status")
    _require(not body.get("error"), "UPSTREAM_ERROR")
    _require(
        status == "completed"
        or (
            status == "incomplete"
            and (body.get("incomplete_details") or {}).get("reason")
            == "max_output_tokens"
        ),
        "UNKNOWN_RESPONSE_STATUS",
    )


def validate_responses_output(items):
    """Reject items the native adapter would discard or cannot replay faithfully."""
    for item in items:
        _require(item.get("type") in {"message", "function_call"}, "OUTPUT_UNSUPPORTED")
        if item.get("type") == "message":
            for block in item.get("content", []):
                _require(
                    block.get("type") in {"output_text", "refusal"},
                    "OUTPUT_UNSUPPORTED",
                )


def validate_request(body, mode):
    """Validate the original JSON; never rewrite, delete, or log its contents."""
    _require(isinstance(mode, str) and mode in {"chat", "responses"}, "INVALID_MODE")
    _object(
        body,
        {
            "model",
            "max_tokens",
            "messages",
            "system",
            "stream",
            "metadata",
            "tools",
            "tool_choice",
            "thinking",
        },
        {"model", "max_tokens", "messages"},
    )
    _require(body["model"] == "harness-openai", "INVALID_MODEL")
    _require(type(body["max_tokens"]) is int and body["max_tokens"] > 0)
    if "stream" in body:
        _require(type(body["stream"]) is bool)
    if "system" in body:
        _text_blocks(body["system"])
    if "metadata" in body:
        _object(body["metadata"], {"user_id"})
        _require(all(isinstance(value, str) for value in body["metadata"].values()))
    if "thinking" in body:
        _require(body["thinking"] == {"type": "disabled"})
    if "tools" in body:
        _require(isinstance(body["tools"], list))
        for tool in body["tools"]:
            _object(
                tool, {"name", "description", "input_schema"}, {"name", "input_schema"}
            )
            _require(
                isinstance(tool["name"], str)
                and re.fullmatch(r"[A-Za-z0-9_-]{1,64}", tool["name"]) is not None
            )
            _require(
                tool["name"] != "web_search" and isinstance(tool["input_schema"], dict)
            )
            _require("description" not in tool or isinstance(tool["description"], str))
    if "tool_choice" in body:
        choice = body["tool_choice"]
        _object(choice, {"type", "name"}, {"type"})
        _require(
            isinstance(choice["type"], str)
            and choice["type"] in {"auto", "any", "tool", "none"}
        )
        _require((choice["type"] == "tool") == ("name" in choice))
        _require("name" not in choice or isinstance(choice["name"], str))
    _require(isinstance(body["messages"], list) and bool(body["messages"]))
    for message in body["messages"]:
        _object(message, {"role", "content"}, {"role", "content"})
        _require(
            isinstance(message["role"], str)
            and message["role"] in {"user", "assistant"}
        )
        content = message["content"]
        if isinstance(content, str):
            continue
        _require(isinstance(content, list) and bool(content))
        kinds = []
        for block in content:
            _require(isinstance(block, dict))
            kind = block.get("type")
            kinds.append(kind)
            if kind == "text":
                _text_blocks([block])
            elif kind == "tool_use" and message["role"] == "assistant":
                _object(
                    block,
                    {"type", "id", "name", "input"},
                    {"type", "id", "name", "input"},
                )
                _require(isinstance(block["id"], str) and bool(block["id"]))
                _require(
                    isinstance(block["name"], str)
                    and re.fullmatch(r"[A-Za-z0-9_-]{1,64}", block["name"]) is not None
                )
                _require(isinstance(block["input"], dict))
            elif kind == "tool_result" and message["role"] == "user":
                _object(
                    block,
                    {"type", "tool_use_id", "content", "is_error"},
                    {"type", "tool_use_id"},
                )
                _require(
                    isinstance(block["tool_use_id"], str) and bool(block["tool_use_id"])
                )
                _require(
                    "is_error" not in block or block["is_error"] is False,
                    "TOOL_ERROR_UNREPRESENTABLE",
                )
                if "content" in block:
                    _text_blocks(block["content"])
                    _require(
                        not isinstance(block["content"], list)
                        or len(block["content"]) == 1
                    )
            else:
                _require(False, "UNSUPPORTED_CONTENT_BLOCK")
        # Both adapters move tool results ahead of user text; Responses also
        # moves assistant function calls ahead of text. Refuse reordered inputs.
        if "tool_result" in kinds:
            _require(
                kinds == sorted(kinds, key=lambda item: item == "text"),
                "CONTENT_ORDER_UNREPRESENTABLE",
            )
        if "tool_use" in kinds and "text" in kinds:
            text_first = mode == "chat"
            _require(
                kinds
                == sorted(
                    kinds,
                    key=lambda item: (
                        (item != "text") if text_first else (item == "text")
                    ),
                ),
                "CONTENT_ORDER_UNREPRESENTABLE",
            )
    try:
        json.dumps(body, allow_nan=False)
    except (ValueError, TypeError):
        _require(False, "INVALID_JSON_VALUE")
