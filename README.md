# ovl

`ovl` は、Overleaf プロジェクトをローカルディレクトリ中心で安全に同期するための CLI です。

基本方針は保守的です。ローカルファイルを主な作業場所として扱いますが、`ovl push` はアップロード前に現在の Overleaf 側の状態を確認します。これにより、Overleaf Web UI で行われた編集を気づかず上書きする事故を避けます。

## 状態

まだ初期実装です。Overleaf は公開されたプロジェクト同期 API を提供していないため、Overleaf backend は editor が使っている非公開 Web endpoint を利用します。これらの endpoint は Overleaf 側の変更で壊れる可能性があります。

内部構造と同期モデルは `INTERNALS.md` にまとめています。現在は clean architecture へ移行中で、domain、port、application、Overleaf adapter を package として分離済みです。

## インストール

### 必要環境

- Git
- Go 1.25.0 以上

Go のバージョンは次のコマンドで確認できます。

```sh
go version
```

リポジトリを clone し、そのルートディレクトリで `go install` を実行します。

```sh
git clone https://github.com/uekann/ovl.git
cd ovl
go install .
```

`go install .` は `ovl` をビルドし、Go の実行ファイル用ディレクトリへインストールします。`GOBIN` が設定されている場合は `$GOBIN/ovl`、未設定の場合は `GOPATH` の先頭ディレクトリにある `bin/ovl`（通常は `$GOPATH/bin/ovl`）に配置されます。`GOPATH` も設定していない通常の構成では、保存先は `$HOME/go/bin/ovl` です。

現在の設定は次のコマンドで確認できます。

```sh
go env GOBIN
go env GOPATH
```

インストール先が `PATH` に含まれていない場合は追加してください。標準設定では、例えば次の行を `~/.profile` や `~/.zshrc` に追加します。

```sh
export PATH="$(go env GOPATH)/bin:$PATH"
```

独自の `GOBIN` を設定している場合は、代わりにそのディレクトリを `PATH` に追加してください。インストール後、任意のディレクトリで動作を確認できます。

```sh
ovl --help
```

インストールせず、clone したリポジトリ内だけでビルドすることもできます。

```sh
go build -o ovl .
./ovl --help
```

## 認証

`ovl` は Overleaf のセッション cookie で認証します。Overleaf のパスワードは保存しません。

デフォルトでは、cookie は現在のプロジェクト内の `.overleaf-sync/auth/cookie` に `0600` 権限で保存されます。Cookie はコマンド引数では渡さず、対話入力します。端末から実行している場合、入力は画面に表示されません。

```sh
ovl auth
```

入力する値は、`overleaf_session2=...` の形式でも cookie 値だけでも構いません。

保存後、`config.json` には cookie 本体ではなく path だけが記録されます。

```text
.overleaf-sync/auth/cookie
```

`.overleaf-sync/` は `ovl init` や `ovl auth` 実行時に `.gitignore` へ自動で追加されます。既に `.gitignore` がある場合は追記します。

任意のファイルに保存し、そのプロジェクトで使うよう設定することもできます。

```sh
ovl auth --file ../ovl-cookie
```

環境変数でも指定できます。環境変数はプロジェクト local の cookie より優先されます。

```sh
export OVL_COOKIE='overleaf_session2=...'
export OVL_COOKIE_FILE=/path/to/cookie
```

cookie が未設定のまま `init`、`pull`、`push` など remote へアクセスする操作を実行するとエラーになります。

help を表示するには:

```sh
ovl --help
ovl push --help
```

self-hosted Overleaf や Server Pro を使う場合:

```sh
ovl init --host https://overleaf.example.com <project-id>
```

## クイックスタート

ローカルプロジェクトディレクトリで実行します。

```sh
ovl auth
ovl init <project-id>
ovl pull
ovl status
ovl comments
ovl compile main.tex
ovl push
```

project ID は Overleaf のプロジェクト URL の末尾です。

```text
https://www.overleaf.com/project/<project-id>
```

## コマンド

### `ovl init <project-id>`

現在のディレクトリを `ovl` プロジェクトとして初期化します。

`init` は保存済み cookie を使って対象 project にアクセスできるか確認します。cookie が未設定、project ID が誤っている、またはそのアカウントから project にアクセスできない場合はエラーで停止します。

作成されるファイルとディレクトリ:

```text
.overleaf-sync/
  config.json
  state.json
  base/
  backups/
.olignore
.gitignore
```

### `ovl pull`

現在の Overleaf プロジェクトをダウンロードし、remote だけで変更されたファイルをローカルへ反映します。

最後の同期以降に local と remote の両方で同じファイルが変更されている場合、ローカルファイルを上書きせず停止します。

実際にファイルを書き換えず、何が起きるか確認するには:

```sh
ovl pull --preview
```

remote 側で削除されたファイルは、デフォルトではローカルから削除しません。削除も反映するには:

```sh
ovl pull --delete-local
```

local の変更を破棄して remote の snapshot に合わせるには:

```sh
ovl pull --force
```

`--force` は、remote に存在するファイルで local を上書きし、remote に存在しない scan 対象の local ファイルを削除します。実行前に local backup を作成します。

### `ovl status`

ローカルファイル、最後に同期した base 状態、現在の Overleaf 状態を比較します。

```sh
ovl status
```

Overleaf にアクセスせず local と base だけを比較するには:

```sh
ovl status --local-only
```

### `ovl diff`

最後に同期した base 状態との差分を表示します。

```sh
ovl diff
ovl diff --remote
ovl diff --all
```

テキストファイルは行単位の unified diff として表示します。変更のない行は前後の context だけを表示します。バイナリファイルは hash と size の要約を表示します。

### `ovl comments [--include-resolved] [--json]`

Overleaf 上でプロジェクトに付けられたコメントを取得して表示します。デフォルトでは、未解決のコメントだけを表示します。

```sh
ovl comments
```

解決済みのコメントも含めるには `--include-resolved` を指定します。

```sh
ovl comments --include-resolved
```

スクリプトなどから扱いやすい JSON 形式で出力するには `--json` を指定します。オプションは組み合わせて使用できます。

```sh
ovl comments --json
ovl comments --include-resolved --json
```

各コメントには、取得できる場合は対象ファイル、文字オフセット、選択されたテキストも表示されます。表示対象になったコメントは、編集によって参照範囲が失われていても省略せず、位置情報を取得できないコメントとして表示します。位置を失った解決済みコメントも確認する場合は `--include-resolved` を指定してください。

コメント本文を取得できても、Overleaf 側のプロジェクト履歴方式や非公開 endpoint の変更によって文書内の位置を取得できない場合があります。その場合もコメント本文は表示し、位置だけを `anchor unavailable` として扱います。

### `ovl compile`

Overleaf 側でコンパイルを実行し、生成された PDF をローカルへ保存します。

```sh
ovl compile
ovl compile main.tex
ovl compile --push main.tex
ovl compile --output build/main.pdf main.tex
```

`root.tex` を指定した場合、そのファイルを root document として compile request に渡します。指定しない場合は Overleaf プロジェクトの現在の root document を使います。

出力先を指定しない場合、`root.tex` 指定時は `<rootのbasename>.pdf`、未指定時は `output.pdf` に保存します。例えば `ovl compile chapters/main.tex` は `main.pdf` を作成します。

`--push` を付けると、コンパイル前に通常の `ovl push` を実行します。remote 変更や conflict がある場合は push と同じく停止し、compile は実行しません。生成される PDF は push 後に保存されるため、その実行で作られた PDF 自体は push 対象になりません。

PDF は生成物なので、新規プロジェクトで作られる `.olignore` には `*.pdf` を含めています。既存プロジェクトで PDF を push 対象にしたくない場合は `.olignore` に `*.pdf` を追加してください。

### `ovl push`

local だけで変更されたファイルを Overleaf へアップロードします。

既存の編集可能な文書は削除せず、同じ Overleaf document として更新します。Overleaf 側で内容の差分が生成されるため、History にはファイルの削除と再作成ではなく、変更した箇所の編集として記録されます。

アップロード前に `ovl` は現在の Overleaf プロジェクトをダウンロードし、最後の同期以降に remote 側が変更されていないか確認します。remote 変更や conflict がある場合、push は拒否されます。

remote 変更を無視して強制的に上書きするには:

```sh
ovl push --force
```

`--force` は実行前に remote backup を作成します。`--force` では local を正として扱うため、remote にだけ存在するファイルや local で削除済みのファイルも remote から削除します。

ローカルで削除されたファイルは、デフォルトでは remote から削除しません。remote 削除も反映するには:

```sh
ovl push --delete
```

`--delete` も実行前に remote backup を作成します。

`--delete` は、最後の同期時点では存在し、現在の local から削除されている remote ファイルを削除します。削除されたファイルだけを含んでいた remote ディレクトリも空になった後に削除します。削除対象ファイルの remote entity を解決できない場合や、削除後の再取得で remote に残っている場合は成功扱いにせず、途中失敗として停止します。

### `ovl backup-remote`

現在の remote 状態を以下へ保存します。

```text
.overleaf-sync/backups/
```

## ignore ルール

`.olignore` は、ローカル scan や push 対象から除外するファイルを指定します。

デフォルトでは以下が含まれます。

```gitignore
.git/
.gitignore
.overleaf-sync/
.direnv/
result/
node_modules/
*.aux
*.log
*.synctex.gz
*.fls
*.fdb_latexmk
*.out
*.pdf
```

## 推奨ワークフロー

1. 普段はローカルで編集する。
2. 履歴管理にはローカル Git を使う。
3. Overleaf へ反映する前に `ovl status` を実行する。
4. `ovl push` でローカル変更を Overleaf へ反映する。
5. 誰かが Overleaf 上で編集した場合は、`ovl pull --preview` の後に `ovl pull` を実行する。
6. ローカルを正として remote を意図的に上書きしたい場合だけ `ovl push --force` を使う。
