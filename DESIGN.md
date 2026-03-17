# grafana-light - デバイス死活・リソース監視ビューア

## 概要

Prometheus + node_exporter で監視しているデバイス群の
CPU / RAM / 温度 / 通信量 / 死活状態をリアルタイムで一覧表示する軽量ビューア。

Raspberry Pi 3B上で既存Grafanaと共存する想定（残リソースで動作）。

表示モードは3系統:

- `server`: ブラウザ向けSSEダッシュボード
- `tui`: ローカル端末向けライブ表示
- `once`: 1回だけスナップショットをCLI表示

## 画面

```
┌─────────────────────────────────────────────────┐
│ grafana-light                            live   │
├──────────┬───────────────┬──────────────┬───────┤
│ Device   │ CPU           │ RAM          │Status │
├──────────┼───────────────┼──────────────┼───────┤
│ raspi-a  │ ████░░ 45.5%  │ ██████░ 62%  │1:alive│
│ raspi-b  │ ██░░░░ 12.3%  │ ███░░░ 31%   │1:alive│
│ raspi-c  │               │              │0:dead │
└──────────┴───────────────┴──────────────┴───────┘
```

## アーキテクチャ

```
 Browser / Terminal ←── grafana-light (Go)
                            │
                            │ instant query (3秒毎)
                            ▼
                       Prometheus
                            │
                 ┌──────────┼──────────┐
                 ▼          ▼          ▼
            node_exporter  node_exporter  ...
            (device A)     (device B)
```

## 技術選定

| 要素 | 選定 | 理由 |
|------|------|------|
| リアルタイム更新 | SSE (Server-Sent Events) | ブラウザ側は最小構成 |
| Prometheusクエリ | instant query (/api/v1/query) | range queryより軽量 |
| HTML生成 | サーバーサイドレンダリング | テーブルHTMLをSSEで送信 |
| TUI描画 | ANSI + x/term (raw mode) | 対話操作・ライブ更新 |
| CSS | インライン | 外部ファイルゼロ |
| JS | EventSource 3行のみ | リアルタイム更新に必要な最小限 |

## 動作フロー

1. Monitor goroutineが3秒毎にPrometheusへクエリを並行発行
   - `up{job="node"}` → 死活
   - `rate(node_cpu_seconds_total{mode="idle"}[1m])` → CPU
   - `node_memory_MemAvailable_bytes / MemTotal` → RAM
   - `node_hwmon_temp_celsius` / `node_thermal_zone_temp` → 温度
   - `node_network_receive_bytes_total` / `node_network_transmit_bytes_total` → 通信量
2. 設定済みだが未取得のデバイスも内部状態に残す
3. 結果を内部状態に保存し、購読者に通知
4. `server` は SSE で配信、`tui` は端末幅に応じて列を増減して再描画

## 実行例

```bash
make run      # ブラウザUI
make run-tui  # ローカル端末UI
make once     # 1回だけCLI表示
```

## 設定

```yaml
server:
  port: 8080
  interval: 3s        # ポーリング間隔

prometheus:
  url: http://localhost:9090
  job: node            # node_exporterのジョブ名

devices:               # 表示名エイリアス（任意）
  "192.168.1.10:9100":
    name: raspi-living
    priority: 220
  "192.168.1.11:9100":
    name: raspi-kitchen
    priority: 180
```

## TUI キーバインド

| キー | 動作 |
|------|------|
| `j` / `↓` | カーソルを下に移動 |
| `k` / `↑` | カーソルを上に移動 |
| `a` | `instance,name,priority` 形式でデバイス登録 |
| `r` | 選択デバイスの名前変更 |
| `p` | 選択デバイスのpriority変更 |
| `o` | priorityソートの昇順 / 降順切替 |
| `m` | 数値ベースソートのON / OFF |
| `s` | 設定をYAMLに保存 |
| `q` / Ctrl+C | 終了 |

名前変更中:

| キー | 動作 |
|------|------|
| 文字入力 | 名前を編集 |
| Enter | 確定 |
| Esc | キャンセル |

## リソース

| 指標 | 値 |
|------|-----|
| バイナリ | 8.5MB (ARM7, stripped) |
| メモリ目標 | < 15MB |
| Go依存 | gopkg.in/yaml.v3, golang.org/x/term |
| JS (serverモード) | 3行 (EventSource) |
| 外部CDN | 0 |
