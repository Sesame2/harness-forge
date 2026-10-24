# 三亚水域公开数据：可执行路径与精度边界

日期：2026-10-25（Asia/Shanghai）。本次仅查阅一手资料并访问公开下载端点，未读取 `.env`、未调用收费模型；未新增数据下载依赖或提交下载的全球数据。

## 结论

短任务中最短、已实际验证的路径是：下载 **Natural Earth 1:1000 万 Ocean v5.1.1** 官方海洋面，用现有 GeoPandas/Shapely 按三亚附近矩形裁剪，导出 GeoJSON 和内嵌 ECharts HTML。它是已有海洋面数据的裁剪，**不是自行用粗略陆地反算海洋，更不是三亚政府实测水域**。[官方数据说明](https://www.naturalearthdata.com/downloads/10m-physical-vectors/10m-ocean/)

成品必须明确标注“三亚近海概览；Natural Earth 1:1000 万概化；非完整内陆水系、非精细海岸调查、非法律/航行边界”。`10m` 指 **10 million 地图比例尺**，不是 10 米空间分辨率；小海湾、河流、池塘和小岛可能省略。[官方维护仓库](https://github.com/nvkelso/natural-earth-vector)

用户已允许获取公开权威数据和调整禁止联网策略，但这不等于允许将项目文件、对话或凭证上传给数据网站。应只读取明确公开数据 URL、保留超时与大小上限；报告显示来源、获取日期、裁剪范围和限制。不自动执行下载到的脚本，不需要第三方账号。

## 已验证的推荐端点

矢量 ZIP：<https://naturalearth.s3.amazonaws.com/10m_physical/ne_10m_ocean.zip>。

官方许可为公共领域，允许修改和再分发，无强制署名；报告仍保留来源便于复核。官网说明其下载托管在 AWS。[使用条款](https://www.naturalearthdata.com/about/terms-of-use/)

本次在项目既有 Python 环境实际执行了 HTTP 下载、Shapefile 读取、WGS84 检查、矩形裁剪、有效性检查和 GeoJSON 序列化，结果：

| 检查 | 实测结果 |
| --- | --- |
| HTTP HEAD | 200；`application/zip`；3,189,765 字节 |
| 完整下载 SHA256 | `db626fcd5d50b096b156c78a2cc95011b39f32a61b4e47d147e3f7a77b8b2719` |
| 输入矩形 | `[108.8, 18.0, 109.9, 18.7]`，经纬度；展示窗口，不是三亚行政区 |
| 坐标系 | EPSG:4326 |
| 裁剪结果 | 1 个有效、非空 Polygon；外环 117 点 |
| 几何实际范围 | `[108.8, 18.0, 109.9, 18.48433490576108]` |
| GeoJSON 序列化 | 4,973 UTF-8 字节；未写入仓库 |

最小处理片段（下载结果只保留内存；输出路径由现有 Run 规范提供）：

```python
import io, urllib.request
import geopandas as gpd
from shapely.geometry import box

url = "https://naturalearth.s3.amazonaws.com/10m_physical/ne_10m_ocean.zip"
with urllib.request.urlopen(url, timeout=30) as response:
    raw = response.read(5 * 1024 * 1024 + 1)
assert len(raw) <= 5 * 1024 * 1024
window = (108.8, 18.0, 109.9, 18.7)
source = gpd.read_file(io.BytesIO(raw), bbox=window)
assert source.crs.to_epsg() == 4326
water = gpd.clip(source, box(*window))
water = water[~water.geometry.is_empty]
assert len(water) and water.geometry.is_valid.all()
geojson = water.to_json(drop_id=True)
```

这只是数据处理参考，不替代宿主的网络访问控制。发布前仍需将来源/比例尺写入每个要素属性及 HTML，并验证页面与下载的 GeoJSON 使用同一份坐标。ECharts 已包含在工作区模板，不需外部 CDN。

网络存在波动：上述 Python 完整下载/转换成功后，另一次 `curl --max-time 40` 下载只收到 1,148,448 字节即超时，截断 ZIP 不可读取。必须把超时/不完整下载当作可见失败，不能发布残缺或捏造数据；短 Run 的网络总时限也需考虑，本文不承诺该公网端点随时能在 40 秒内完成。

维护者仓库也提供 [直接 GeoJSON 文件](https://github.com/nvkelso/natural-earth-vector/blob/master/geojson/ne_10m_ocean.geojson)（约 9.68 MB）；本机访问 raw.githubusercontent.com 出现连接重置，因此不能将该端点标为下载成功，也不作为本次优先路径。官网 Ocean 页的旧下载超链接本次解析为错误 URL，故明确使用上面已验证的 AWS 端点。

## 更高精度候选及为何不作本次默认

| 数据 | 一手来源与许可 | 本次判断 |
| --- | --- | --- |
| JRC Global Surface Water | 欧委会 JRC 的 Landsat 衍生水面时序；免费使用但要求引用，地图署名 `Source: EC JRC/Google`。公开交付包含 10°×10° GeoTIFF、Earth Engine、WMTS。[官方访问及许可](https://global-surface-water.appspot.com/download) | 政府研究机构数据，适合后续精细地表水。它不是现成三亚 GeoJSON；需下载/裁剪栅格、明确定义阈值与年份再矢量化，当前 Runtime 未安装栅格处理库。本次未下载、未验证三亚分类结果；WMTS RGB 图片不能替代可分析矢量。 |
| GSHHG 2.3.7 | 夏威夷大学/NOAA 维护；WGS84 分级海岸/湖岸，LGPL。提供 Shapefile。[官方说明](https://www.soest.hawaii.edu/wessel/gshhg/)、[NOAA 元数据](https://www.ngdc.noaa.gov/mgg/shorelines/shorelines.html) | [维护者 ZIP](https://www.soest.hawaii.edu/pwessel/gshhg/gshhg-shp-2.3.7.zip) 实测 HEAD 200、149,157,845 字节；未下载全量。体积大、需处理陆/海/湖层级，且 2017 版不等于当前实测三亚岸线，不能在本次只读研究中声称局部验收成功。 |
| NASA/USGS SRTM Water Body V003 | 官方目录描述约 30 米产品、Shapefile/栅格，观测时段为 2000 年；公共政府作品。[NASA 官方目录记录](https://catalog.data.gov/dataset/nasa-shuttle-radar-topography-mission-water-body-data-shapefiles-raster-files-v003)、[USGS 访问说明](https://www.usgs.gov/centers/eros/science/usgs-eros-archive-digital-elevation-shuttle-radar-topography-mission-water) | 需由 Earthdata/EarthExplorer 获取对应瓦片；本次没有验证匿名瓦片下载，不把页面“public”误当作无需登录的可用端点，也不适合作为无需新增凭证的短回归前提。 |

本次没有找到并验证“三亚官方完整水域 GeoJSON、匿名下载、开放再分发”的端点。不得据此推断该数据不存在。若用户需要城市河网、湖库面积、现势岸线或法定水域，应另行选定相应数据源与精度口径，而非扩大 Natural Earth 的结论范围。
