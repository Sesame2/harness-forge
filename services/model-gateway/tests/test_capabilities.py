"""Pure request-boundary checks; no Docker, network, SDK, or credentials."""

import copy
import importlib.util
from pathlib import Path
import unittest


SOURCE = Path(__file__).resolve().parents[1] / "capabilities.py"


class CapabilityContract(unittest.TestCase):
    def setUp(self):
        self.assertTrue(SOURCE.is_file(), "Missing fail-closed capability validator")
        spec = importlib.util.spec_from_file_location("capabilities", SOURCE)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        self.validate = module.validate_request
        self.usage = getattr(module, "validate_usage", None)
        self.response_status = getattr(module, "validate_response_status", None)
        self.response_output = getattr(module, "validate_responses_output", None)
        self.chat_output = getattr(module, "validate_chat_output", None)
        self.request = {
            "model": "harness-openai",
            "max_tokens": 64,
            "messages": [{"role": "user", "content": " \nkeep whitespace \t"}],
        }

    def test_sdk_text_and_function_tool_subset_is_accepted_without_mutation(self):
        self.request.update(
            {
                "stream": True,
                "thinking": {"type": "disabled"},
                "system": [
                    {"type": "text", "text": "first\n"},
                    {"type": "text", "text": " second"},
                ],
                "metadata": {"user_id": "synthetic"},
                "tools": [
                    {
                        "name": "lookup",
                        "description": "local tool",
                        "input_schema": {
                            "type": "object",
                            "properties": {"query": {"type": "string"}},
                            "required": ["query"],
                        },
                    }
                ],
            }
        )
        original = copy.deepcopy(self.request)
        for mode in ("chat", "responses"):
            self.assertIsNone(self.validate(self.request, mode))
        self.assertEqual(self.request, original)

    def test_unknown_top_level_or_semantic_parameter_is_rejected(self):
        for name, value in {
            "bogus": True,
            "top_k": 1,
            "stop_sequences": ["stop"],
            "context_management": {},
            "cache_control": {},
            "output_config": {"effort": "high"},
            "proxy_server_request": {"body": {}},
            "user": "spoof",
            "callbacks": [],
            "litellm_metadata": {},
        }.items():
            with self.subTest(name=name):
                request = {**self.request, name: value}
                with self.assertRaisesRegex(ValueError, "^HF_GATEWAY_"):
                    self.validate(request, "chat")

    def test_unknown_nested_fields_or_native_blocks_are_rejected(self):
        mutations = [
            {"messages": [{"role": [], "content": "text"}]},
            {"tool_choice": {"type": []}},
            {"messages": [{"role": "user", "content": "text", "bogus": True}]},
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
            {"tools": [{"name": "tool", "type": "bash_20250124", "input_schema": {}}]},
            {"tools": [{"name": "tool", "input_schema": {}, "bogus": True}]},
            {"metadata": {"user_api_key_user_id": "spoof"}},
            {"thinking": {"type": "enabled", "budget_tokens": 1024}},
            {"tool_choice": {"type": "auto", "bogus": True}},
            {"tool_choice": {"type": "auto", "disable_parallel_tool_use": True}},
        ]
        for mutation in mutations:
            for mode in ("chat", "responses"):
                with self.subTest(mutation=mutation, mode=mode):
                    with self.assertRaisesRegex(ValueError, "^HF_GATEWAY_"):
                        self.validate({**self.request, **mutation}, mode)

    def test_error_tool_result_and_reordered_mixed_blocks_are_rejected(self):
        for content in [
            [
                {
                    "type": "tool_result",
                    "tool_use_id": "call-1",
                    "content": "failed",
                    "is_error": True,
                }
            ],
            [
                {"type": "text", "text": "before"},
                {"type": "tool_result", "tool_use_id": "call-1", "content": "ok"},
            ],
        ]:
            with self.subTest(content=content):
                request = {
                    **self.request,
                    "messages": [{"role": "user", "content": content}],
                }
                with self.assertRaisesRegex(ValueError, "^HF_GATEWAY_"):
                    self.validate(request, "responses")

    def test_tool_history_preserves_json_and_successful_result(self):
        self.request["messages"].extend(
            [
                {
                    "role": "assistant",
                    "content": [
                        {
                            "type": "tool_use",
                            "id": "call-1",
                            "name": "lookup",
                            "input": {"nested": [False, None, 1, "\n"]},
                        }
                    ],
                },
                {
                    "role": "user",
                    "content": [
                        {
                            "type": "tool_result",
                            "tool_use_id": "call-1",
                            "content": "  result\n",
                            "is_error": False,
                        }
                    ],
                },
            ]
        )
        original = copy.deepcopy(self.request)
        for mode in ("chat", "responses"):
            self.validate(self.request, mode)
        self.assertEqual(self.request, original)

    def test_usage_requires_actual_nonnegative_integer_counts(self):
        self.assertIsNotNone(self.usage, "Missing upstream usage validation")
        for mode, names in (
            ("chat", ("prompt_tokens", "completion_tokens")),
            ("responses", ("input_tokens", "output_tokens")),
        ):
            actual = dict(zip(names, (7, 3)))
            self.usage(actual, mode)
            self.usage(dict(zip(names, (0, 0))), mode)
            for usage in (
                None,
                {},
                {**actual, names[0]: -1},
                {**actual, names[1]: True},
                {**actual, names[0]: "7"},
            ):
                with self.subTest(mode=mode, usage=usage):
                    with self.assertRaisesRegex(ValueError, "^HF_GATEWAY_"):
                        self.usage(usage, mode)

    def test_responses_status_cannot_turn_failure_into_success(self):
        self.assertIsNotNone(self.response_status)
        self.response_status({"status": "completed"})
        self.response_status(
            {
                "status": "incomplete",
                "incomplete_details": {"reason": "max_output_tokens"},
            }
        )
        for body in (
            {},
            {"status": "failed"},
            {"status": "in_progress"},
            {"status": "unknown"},
            {"status": "completed", "error": {"code": "server_error"}},
            {
                "status": "incomplete",
                "incomplete_details": {"reason": "content_filter"},
            },
        ):
            with (
                self.subTest(body=body),
                self.assertRaisesRegex(ValueError, "^HF_GATEWAY_"),
            ):
                self.response_status(body)

    def test_responses_nested_unknown_content_cannot_be_dropped(self):
        self.assertIsNotNone(self.response_output)
        with self.assertRaisesRegex(ValueError, "^HF_GATEWAY_"):
            self.response_output(
                [
                    {
                        "type": "message",
                        "content": [{"type": "output_audio", "data": "synthetic"}],
                    }
                ]
            )

    def test_chat_raw_tool_json_id_and_audio_are_not_silently_rewritten(self):
        self.assertIsNotNone(self.chat_output)
        call = {
            "id": "call-valid_1",
            "type": "function",
            "function": {"name": "lookup", "arguments": "{}"},
        }
        message = {"role": "assistant", "content": None, "tool_calls": [call]}
        self.chat_output({"choices": [{"message": message}]}, False)
        self.chat_output(
            {
                "choices": [
                    {
                        "delta": {
                            "tool_calls": [
                                {
                                    "index": 0,
                                    "id": None,
                                    "type": None,
                                    "function": {"arguments": '{"part":'},
                                }
                            ]
                        }
                    }
                ]
            },
            True,
        )
        for bad in ('{"x":1', "", "   ", "[]", '{"x":NaN}'):
            with (
                self.subTest(arguments=bad),
                self.assertRaisesRegex(ValueError, "^HF_GATEWAY_"),
            ):
                self.chat_output(
                    {
                        "choices": [
                            {
                                "message": {
                                    **message,
                                    "tool_calls": [
                                        {
                                            **call,
                                            "function": {
                                                "name": "lookup",
                                                "arguments": bad,
                                            },
                                        }
                                    ],
                                }
                            }
                        ]
                    },
                    False,
                )
        for identifier in ("call.1", "call:1", "", "call__thought__signature"):
            for stream in (False, True):
                with (
                    self.subTest(identifier=identifier, stream=stream),
                    self.assertRaisesRegex(ValueError, "^HF_GATEWAY_"),
                ):
                    self.chat_output(
                        {
                            "choices": [
                                {
                                    "delta" if stream else "message": {
                                        **message,
                                        "tool_calls": [{**call, "id": identifier}],
                                    }
                                }
                            ]
                        },
                        stream,
                    )
        self.chat_output(
            {
                "choices": [
                    {"message": {**message, "tool_calls": [{**call, "id": "a" * 256}]}}
                ]
            },
            False,
        )
        for stream in (False, True):
            with (
                self.subTest(audio_stream=stream),
                self.assertRaisesRegex(ValueError, "^HF_GATEWAY_"),
            ):
                self.chat_output(
                    {
                        "choices": [
                            {
                                "delta" if stream else "message": {
                                    "content": "retained text",
                                    "audio": {
                                        "id": "aud-synthetic",
                                        "data": "YQ==",
                                        "transcript": "audio text",
                                        "expires_at": 1,
                                    },
                                }
                            }
                        ]
                    },
                    stream,
                )


if __name__ == "__main__":
    unittest.main()
