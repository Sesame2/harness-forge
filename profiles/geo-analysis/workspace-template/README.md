# Geo Analysis Workspace

Use this workspace for analysis code and intermediate files. Project inputs live
in the read-only sibling `../inputs`; publish only to the sibling `../outputs`.

The primary report is `outputs/report/index.html`. Copy `assets/echarts.min.js`
and `assets/LICENSE.echarts.txt` to `outputs/report/vendor/` and load the chart
library with `./vendor/echarts.min.js`. Reports must work without a CDN.
