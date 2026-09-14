#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
cd "$tmp"
npm pack echarts@5.6.0 --ignore-scripts --silent >/dev/null
tar -xzf echarts-5.6.0.tgz package/dist/echarts.min.js package/LICENSE
printf '%s\n' \
  'bf4a223524e40b77c304bec67e1222cf551f14880cf42c69dc046558e11c07b1  package/dist/echarts.min.js' \
  '634293835b43a6dd2094fa39182a3d9a6b9ca43b7fdb9ac354e8037af2a3093a  package/LICENSE' \
  | shasum -a 256 --check
destination="$root/profiles/geo-analysis/workspace-template/assets"
mkdir -p "$destination"
cp package/dist/echarts.min.js "$destination/echarts.min.js"
cp package/LICENSE "$destination/LICENSE.echarts.txt"
