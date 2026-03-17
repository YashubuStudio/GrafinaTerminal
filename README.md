# grafana-light

Prometheus + node_exporter で監視しているデバイス群の CPU / RAM / 死活状態を軽く表示するビューアです。

表示モードは 3 つあります。

- `server`: ブラウザ向けの軽量ダッシュボード
- `tui`: ローカル端末向けのライブ表示
- `once`: 1 回だけ状態を出して終了

Grafana の代替というより、低スペック機で「今どのノードが生きていて、CPU/RAM がどのくらいか」を一覧したい用途向けです。

## 前提

- Prometheus が HTTP API で参照可能であること
- Prometheus が `node_exporter` を scrape していること
- `job` 名が設定ファイルと一致していること

注意:

- `prometheus.url` に入れるのは `node_exporter` の URL ではなく Prometheus の URL です
- `192.168.x.x:9100` は通常 `node_exporter` です
- `grafana-light` は Prometheus API `/api/v1/query` を使います

## 設定

設定ファイル例は [configs/example.yaml](configs/example.yaml) にあります。

```yaml
server:
  port: 8080
  interval: 3s
  burn_in:
    enabled: true
    pixel_shift_interval: 45s
    pixel_shift_step: 2
    idle_dim_after: 90s
    idle_brightness: 0.65

prometheus:
  url: http://localhost:9090
  job: node

devices:
  "192.168.0.101:9100":
    name: node-101
    priority: 220
  "192.168.0.212:9100":
    name: node-212
    priority: 180
```

- `server.port`: `server` モードで待ち受けるポート
- `server.interval`: Prometheus を引く間隔。正の duration が必要です
- `server.burn_in.enabled`: ブラウザ表示の焼き付き対策を有効化するか
- `server.burn_in.pixel_shift_interval`: 画面全体を微小移動する間隔
- `server.burn_in.pixel_shift_step`: 1 回あたりの移動量 (px)
- `server.burn_in.idle_dim_after`: 無操作時に減光へ入るまでの時間
- `server.burn_in.idle_brightness`: 減光時の明るさ。`0 < x <= 1`
- `prometheus.url`: Prometheus 本体の URL
- `prometheus.job`: `node_exporter` の job 名
- `devices.<instance>.name`: 表示名
- `devices.<instance>.priority`: 0-255 の優先度。通常表示では大きい方が先

`devices` は旧形式の `instance: "name"` でも読み込めますが、保存すると新しいオブジェクト形式になります。

## 焼き付き対策

`server` モードでは、焼き付き対策として次を入れています。

- 数十秒おきの `pixel shift`
- 無操作時の自動減光
- 保護用の低輝度テーマ

常時表示のモニタで使う場合は、まずデフォルト設定のまま試し、必要なら `server.burn_in.*` を詰めてください。

## ローカル実行

ビルド:

```bash
make build
```

ブラウザ表示:

```bash
./bin/grafana-light -mode server -config configs/example.yaml
```

TUI 表示:

```bash
./bin/grafana-light -mode tui -config configs/example.yaml
```

1 回だけ表示:

```bash
./bin/grafana-light -mode once -config configs/example.yaml
```

`Makefile` からも実行できます。

```bash
make run
make run-tui
make once
```

## TUI 操作

- `j` / `k`: カーソル移動
- `↑` / `↓`: カーソル移動
- `a`: `instance,name,priority` 形式でデバイス登録
- `r`: 選択中デバイスの表示名を変更
- `p`: 選択中デバイスの priority を変更
- `o`: priority ソートの昇順 / 降順を切り替え
- `m`: 数値ベースのソートを ON / OFF
- `s`: 設定ファイルへ保存
- `q`: 終了

補足:

- `r` と `p` と `a` の変更はメモリ上に即時反映されます
- `s` を押すまで設定ファイルには保存されません
- TUI の `save` は、指定した設定ファイルに書き込み権限が必要です
- 数値ベースのソートは CPU / RAM / 温度 / 通信量のうち、その時点で最も割合が高い値を優先します
- ターミナル幅が広いと温度と RX / TX を表示します

## ビルド

開発機向け:

```bash
make build
```

Raspberry Pi 3B / ARMv7 向け:

```bash
make build-arm
```

生成物:

- [bin/grafana-light](bin/grafana-light)
- [bin/grafana-light-arm7](bin/grafana-light-arm7)

## Raspberry Pi OS への導入

ここでは Raspberry Pi OS 上で `server` モードを systemd サービスとして常駐させる手順と、`tui` モードを手動で使う手順を分けて説明します。

### 1. ARM 向けバイナリを作る

開発機側で:

```bash
make build-arm
```

### 2. Raspberry Pi へファイルを転送する

開発機側で:

```bash
scp bin/grafana-light-arm7 pi@<raspi-host>:/tmp/grafana-light
scp configs/example.yaml pi@<raspi-host>:/tmp/config.yaml
scp configs/grafana-light.service pi@<raspi-host>:/tmp/grafana-light.service
```

### 3. Raspberry Pi 上で配置する

Raspberry Pi 側で:

```bash
sudo mkdir -p /opt/grafana-light /etc/grafana-light
sudo install -m 0755 /tmp/grafana-light /opt/grafana-light/grafana-light
sudo install -m 0644 /tmp/config.yaml /etc/grafana-light/config.yaml
sudo install -m 0644 /tmp/grafana-light.service /etc/systemd/system/grafana-light.service
```

### 4. 設定ファイルを編集する

Raspberry Pi 側で:

```bash
sudoedit /etc/grafana-light/config.yaml
```

最低限確認する項目:

- `prometheus.url` が本物の Prometheus を向いているか
- `prometheus.job` が実際の scrape job 名と一致しているか
- `devices` の `instance:port` が Prometheus 上の `instance` ラベルと一致しているか
- `priority` を付ける場合は 0-255 の範囲か

### 5. service を有効化して起動する

Raspberry Pi 側で:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now grafana-light
sudo systemctl status grafana-light
```

ログ確認:

```bash
journalctl -u grafana-light -f
```

ブラウザ確認:

```text
http://<raspi-host>:8080/
```

systemd ユニットの元ファイルは [configs/grafana-light.service](configs/grafana-light.service) です。

### 6. TUI として使う場合

TUI は対話前提なので、通常は systemd 常駐より手動起動の方が自然です。

Raspberry Pi 側で:

```bash
/opt/grafana-light/grafana-light -mode tui -config /etc/grafana-light/config.yaml
```

SSH 越しに常用するなら `tmux` や `screen` の中で起動するのが扱いやすいです。

### 7. one-shot で状態だけ見たい場合

Raspberry Pi 側で:

```bash
/opt/grafana-light/grafana-light -mode once -config /etc/grafana-light/config.yaml
```

cron や手動確認向けです。

## 動作確認のポイント

Prometheus の疎通確認:

```bash
curl http://<prometheus-host>:9090/-/ready
curl -G --data-urlencode 'query=up{job="node"}' http://<prometheus-host>:9090/api/v1/query
```

よくある詰まり方:

- `Connection refused`: Prometheus ではなく node_exporter や閉じたポートを向いている
- デバイスが出ない: `prometheus.job` が合っていない
- 名前が変わらない: `devices` のキーが Prometheus 上の `instance` と一致していない
- 温度が出ない: 対象ノードが `node_hwmon_temp_celsius` / `node_thermal_zone_temp` を持っていない
- 通信量が出ない: 対象ノードのネットワークメトリクスが取れていないか、対象インターフェースが除外されている
- TUI で保存できない: 設定ファイルの書き込み権限がない

## 開発時の確認

```bash
go test ./...
go test -race ./...
go vet ./...
```

設計メモは [DESIGN.md](DESIGN.md) にあります。
