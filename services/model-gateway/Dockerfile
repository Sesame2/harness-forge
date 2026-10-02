FROM docker.litellm.ai/berriai/litellm:v1.103.2@sha256:f63fb81b831b170ec16851e23c36ac5bf52ef106b271406429524a2ed730bbfd

COPY upstream-errors.patch /tmp/upstream-errors.patch
# Exact source hashes plus zero-fuzz patching make upstream version drift fail
# the build. The patch changes error handling, never protocol field mappings.
RUN apk add --no-cache --virtual .patch-build patch \
    && cd /app/.venv/lib/python3.13/site-packages \
    && echo 'a427afe224de9d24091158c841864884f698ec218bb0b04389bed35985da7d06  litellm/litellm_core_utils/core_helpers.py' | sha256sum -c - \
    && echo '3d1d031111d880fb6195e741c6fa9c009d1e2f48fa6a4d1ae17724ca0b8f6824  litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py' | sha256sum -c - \
    && echo '2104f838068fda3610dfb74cbcd8ea95867a3da54fefcb7458de748afeeaadd5  litellm/llms/anthropic/experimental_pass_through/responses_adapters/transformation.py' | sha256sum -c - \
    && echo 'a4e630f212320b8c5871f6f6a8219f96459cde4cd3c03fed8582f813fd71eb29  litellm/litellm_core_utils/streaming_handler.py' | sha256sum -c - \
    && echo '303a5f0b82318e8f87d0c3b31db791e8370d41cc069741dd503b8a6e2b31948b  litellm/litellm_core_utils/llm_response_utils/convert_dict_to_response.py' | sha256sum -c - \
    && echo '15423d1e0bdf302426c23c44d98c83240caba55e1c7fa418eadbdd5c2d7bb872  litellm/llms/openai/responses/transformation.py' | sha256sum -c - \
    && echo '25aa107aa13ff277e8bb544dfde1c4135c1fe91e949e7e83a41628ad3f6706e6  litellm/proxy/anthropic_endpoints/endpoints.py' | sha256sum -c - \
    && python -c 'import openai; assert openai.__version__ == "2.33.0"' \
    && echo '3ff1f84c4fd76d5cc0abb9e279da67265cada89b3beaf91a70cb053229a5622c  openai/_base_client.py' | sha256sum -c - \
    && patch --batch --fuzz=0 -p1 < /tmp/upstream-errors.patch \
    && rm /tmp/upstream-errors.patch \
    && apk del .patch-build

COPY capabilities.py callback.py config-chat.yaml config-responses.yaml /app/gateway/
ENV PYTHONPATH=/app/gateway DO_NOT_TRACK=true LITELLM_LOCAL_MODEL_COST_MAP=true LITELLM_LOG=CRITICAL
CMD ["--config", "/app/gateway/config-chat.yaml", "--port", "4000"]
