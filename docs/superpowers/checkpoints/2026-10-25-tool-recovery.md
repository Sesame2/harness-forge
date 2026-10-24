# 工具自修复与Geo Profile：恢复 checkpoint

日期：2026-10-25（Asia/Shanghai）。承接 main/origin/实施树 `1ff3596265b6bdeec7e1c3fec93e9f633e75d095`。最新计划：[Task29–31](../plans/2026-10-25-tool-recovery.md)。此页是本次恢复入口，旧Task1–28历史不重做。

## 已批准边界与实现

- 用户允许调整“工具错误即拒绝”：失败结果以JSON字符串显式携带 `is_error:true` 和原content（支持字符串/文本块列表/缺省），两个协议仍用固定LiteLLM v1.103.2转换器，tool ID关联不变。第三份窄补丁保留原hash/zero-fuzz；不新写转换器，成功结果不变，非布尔状态仍拒绝。
- 同一个Run累计前2个失败工具结果允许模型修正，第三个以 `tool_recovery_limit` 失败；不创建新Run、不fallback、不保证模型能修好。失败步骤自动展开。只公开已知异常类别、ASCII文件名和行号或退出码，不展示任意原始异常正文；父进程保留已验证的具体终态，取消/畸形流/尾部故障仍失败。
- 用户最新要求Geo Profile最大 **50轮**，覆盖中途10轮方案；兼容输出8192、预算配置2.0不变。SDK美元预算并不是第三方准确费用硬上限，CLI内部恢复分支也不完全受单一retry参数约束。
- 用户允许获取公开权威数据。Geo Profile经Bash/Python下载任务相关公开HTTPS数据，保留来源/许可/日期/CRS/精度/原始字节digest；不安装包、读取凭证或修改inputs。报告与GeoJSON离线运行。此为模型策略，当前Docker没有出站防火墙，不能冒称强制SSRF/网络隔离。
- [一手来源研究](../../research/2026-10-25-sanya-water-data.md)：官方Natural Earth Ocean可实际下载/裁剪为有效GeoJSON，但1:1000万只是近海概览，非精细海岸、完整河湖或法定水域。联网不授权向数据站上传用户文件/对话/密钥。

## 原始失败与真实回归

原提问严格为“画一个三亚的水域图，用geojson展示出来”，原Run `c53c18cb-cd6d-4da1-bace-46a0bd9510ed`，原Conversation `7ac1f7aa-3739-4537-8fe2-5edecb19dc59`，Project `5f0db005-9abe-4cde-a03d-7ce3893b13d8`。保留原数据/工作目录，不改历史。

1. 原脚本 `analysis.py:57` 通过只读compile稳定复现SyntaxError；原网关 `HF_GATEWAY_TOOL_ERROR_UNREPRESENTABLE` 拒绝失败工具历史，阻断修正；Runtime/父进程又覆盖成通用worker_failed。
2. 修复后首次原样回放Run `de414ee6-2445-42b2-bf26-2b1a1ec2741f`，Conversation `e1d36d4b-a490-4f02-ac3f-1022c034da5a`：真实模型收到两个SyntaxError并修正，后续工具成功，但原8轮耗尽，明确 `agent_turn_limit`。未发布，不算完整PASS。仅江浙沪CSV且旧离线规则，不能生成真正三亚水域。
3. 用户允许联网并设50轮后，Run `f08a5038-783b-460c-8e50-47c235dfd385`，Conversation `24c84477-8f74-42f6-a503-e63c4aca3686`：文件路径、缺少unzip、地理库方法调用连续触发错误，第三次以 `tool_recovery_limit` 停止。确证有界停止与具体终态有效，不算PASS。补充通用绝对路径/Python zipfile/GeoSeries CRS指引及AttributeError安全展示后显式追加一次回归，无无限自动重试。
4. Run `8413c8c7-e1f9-4493-8b09-4d3e692e7635`，Conversation `c7719b54-f312-4ef0-abf4-38c329e27360`：模型额外展开行政区/水面面积计算，首次失败修正后工具成功，后续ImportError/AttributeError使第三次失败触发终止。已有GeoJSON中间文件但未发布，不算PASS。补充无冒号AssertionError/ImportError安全分类（先RED后GREEN），不改纠错次数；通用Profile优先最小请求成果，不自行增加行政区/面积分析或混用未经核实的CRS。追加回归Run `56fb3bd9-c2a6-48af-9b17-9e62a86792bd`，Conversation `b4ebd46e-9fbd-42ac-a71d-8bdc847f9303`，仍不断查询Overpass细节，经历FileNotFoundError/ValueError后继续，约6分钟由本次测试主动取消并确认finalized；不是模型成功或达到50轮。
5. 普通提问“请分析项目资料里各地点的 value，给我一个图表和总结。”：Run `6dcd9a57-d2e3-48f4-91a6-b6ef57b5c003`，Conversation `6b3b9cb5-042a-4e6d-aa77-b7dfa6d344ac`，**真实PASS**。两次 `AttributeError（build.py:129）` 均由模型收到失败结果后自行Edit修正，后续Bash成功，Run succeeded/finalized并发布报告/GeoJSON/evidence；没有手改模型脚本或重写历史。
   - 主HTML `b9a8e91f-1050-4cc2-8497-44afc3e709e6`；GeoJSON `645ed3ea-2ae2-4822-b3ff-df8ef7ea62ee`；evidence `ba000aac-9245-4210-9bb3-94af546643e9`。
   - evidence原始digest与Project input API相同（`a3f85d0a…b93e2`），4行/value_sum54/mean13.5；4个实际经纬度Point及其value一致。
   - 浏览器回放真实页面：失败步骤2个自动展开、具体文件/行号可见；ECharts674×400、绘制对象35，无pageerror或外部资源请求。截图 `/tmp/hf-tool-recovery-ordinary.png` 已由主代理目视核查。此轮由API提交原始自然提问、浏览器读取真实成果；不是声称经UI点击发送，UI发送另有Fake E2E覆盖。
6. 公开数据准备：复用Run已下载的官方Ocean ZIP，验证SHA256与一手源研究完全一致，现有GeoPandas裁剪bbox `[108.8,18.0,109.9,18.7]`，得到1个有效非空Polygon。来源/许可/访问日期/比例尺/原始归档digest/非完整内陆水系与非行政边界说明保存在要素属性。通过现有上传API追加Project资料 `sanya-nearshore-natural-earth.geojson`（5545字节），Input `271725ec-66e2-4789-8da8-1b941a204342`、digest `b88855f1ec84f7789ff259e2af47e1f3d8836d04f247cc809c1e16e4d646c0b6`；原CSV及所有历史保持不变。此为用户已允许的公开权威数据准备，不冒称模型独立下载成功或三亚官方完整水域数据。Profile优先既有相关GeoJSON，避免重复下载。
7. 原提问再次逐字回放，Run `35bb3bc8-49ae-44d7-a322-6d84b4787e7d`、Conversation `6f3fbb9b-5f5b-40ef-92b9-c5574ae81209` **真实PASS**（succeeded/finalized）。本次手工测试设180s轮询/取消界限，并非Runtime产品自动deadline；50轮/两次纠错边界不变。
   - 两次工具错误为 `ValueError（common.py:573）` 和 `AttributeError（build.py:101）`，模型收到后自行修正，后续工具与发布成功；没有手改生成脚本/制品。累计本轮5个原提问Run（首个8轮、其余50轮，含1个主动取消）和1个普通提问Run，失败未隐去，没有自动付费重跑。
   - 主HTML `783451ec-cad5-424e-bc7c-c0c391c557a4`；GeoJSON `0c8099c5-9999-4601-9a3f-869840b1e91c`；evidence `2de8c108-68f8-4b1c-9566-49b75bc3da0c`。输出1个有效Polygon，几何与官方海洋面独立裁剪结果拓扑完全相等，未编造坐标；原始117顶点，报告渲染简化为94顶点且已说明。输出保留来源/许可/归档digest等属性。
   - 两份输入digest准确匹配；原CSV4行被正确标为不适用于三亚。独立EPSG:32649计算海洋面几何面积4201.73km²，与evidence一致；这是裁剪研究窗口内的概化海洋面，不是三亚行政水域面积。报告明确Natural Earth、Public domain、1:1000万、非行政边界及不包含完整内陆水系。
   - 浏览器真实页面两图实际绘制：地图662×460/48绘制对象、面积图662×300/33对象；2条具体错误自动展开，无pageerror/外部请求。GeoJSON tab可切换、下载HTTP200且attachment，刷新后事件与具体错误重放通过。主代理已目视检查 `/tmp/hf-tool-recovery-sanya.png` 和独立地图截图 `/tmp/hf-tool-recovery-sanya-map.png`。
   - 首次人工QA把“1:1000 万”中的空格误判为缺比例尺，另一次在异步事件加载前读取数量；分别使用允许空格的匹配与显式等待后通过，不改产品/制品或放宽几何、绘制等闸门。

## 确定性验证

- 能力校验先RED；两协议实际网关HTTP工具失败合同先RED（14.576s）后GREEN（17.816s），包括stream/nonstream、string/list/缺省content及ID；完整固定网关7/7通过（125.210s），最终重复7/7通过（112.600s）。
- SDK/runner/父进程/Web均有RED→GREEN证据；额外SDK→runner失败后成功发布、fork源不变回归通过。AttributeError展示先RED再GREEN。
- 最终默认Go全包、Python297（7.32s）、Vitest **109**、Node **79**通过；最后安全分类补充后Python **299**（6.05s），新非布尔状态负例加入后纯网关 **14**通过。Ruff/Mypy13源文件、Web production build、layout、diff check通过。
- 最新真实PostgreSQL/MinIO integration通过，权限用例0.58s，无skip/cache；最新Fake E2E5/5（25.0s）。两项目自有容器/卷/网络已finally清理，最终标签复查全部0。
- 不把旧独立云CI或旧双协议真实smoke冒称当前新增能力验收。当前新增错误封装双协议有实际HTTP合同；本次自然提问付费回归用本机Chat配置。原生真实Claude/新网关独立CI未运行。

## 保留环境与恢复操作

本机Compose项目 `harness-forge-openai`，Docker Provider、真实OpenAI Chat，模型仅记录hash `7d963f65d1ed`；Web5173、Go8080/8081、Runtime8090、PG5432、MinIO9000/9001均仅绑定127.0.0.1，网关4000不发布。所有旧数据保留，不得down-v。旧Fake项目已停止，其4个数据卷保留；不动其它历史项目。

真实 `.env` 仅主目录 `/Users/mei/Desktop/Project/harness-forge/.env`，600、忽略、未跟踪；不复制或输出。恢复时先查Git与未finalize Run；无在途Run才更新服务，不自动重发付费任务。已核对运行容器与实施源码的Profile/提示/Runtime SHA256完全一致，并验证Docker Provider、openai-chat及Runtime没有OPENAI_API_KEY。所有已批准功能及本机验收已完成，不自动重做历史任务或调用收费smoke。

测试入口：<http://localhost:5173/projects/5f0db005-9abe-4cde-a03d-7ce3893b13d8/conversations/6f3fbb9b-5f5b-40ef-92b9-c5574ae81209>。普通提问成功会话为同Project下 `6b3b9cb5-042a-4e6d-aa77-b7dfa6d344ac`。当前进程来自实施树构建的镜像，不绑定Web/Runtime源码；快进main不需要中断或再跑模型，后续可在main用同一 `harness-forge-openai` 开发项目启动。

## 交付确认与下次恢复

- Task29–31全部完成。验收实现冻结为 `166e977f8d71bbb972d4c4ba5241482bf381cee6`，已快进主目录main并push；`git ls-remote origin refs/heads/main`核对同一SHA，主目录及实施树当时均干净。随后的提交仅完成本段和计划checkbox，不改变已验收实现；最新交付SHA以Git main/远端为准。
- 提交前25个staged文件检查通过；实际key/上游URL/模型原值4项扫描无泄漏，`.env`没有入库。临时integration/e2e/网关合同资源清理，不删除保留开发卷或其它历史项目。保留环境6个运行服务全部healthy、仅本机绑定，最后检查无未finalize Run。
- 下次说“继续”时先读本页、查看Git/运行状态，再承接新需求；不要重跑本页历史任务或自动调用付费模型。50轮只决定总轮数上限，第三次工具失败仍会停止；普通自然提问和此数据范围内的三亚近海概览已实测，但不是保证任意模型代码或任意地理分析都成功。
- 未测/不在范围：新增功能独立云CI、原生真实Claude、当前错误封装的Responses付费自然任务、无缓存fresh clone、精细完整三亚水系、强制网络防火墙、E2B实现和多租户认证。旧双协议真实验收、当前双协议HTTP合同与本次Chat自然任务必须分开表述，不互相冒充。
