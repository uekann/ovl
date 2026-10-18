# ovl の開発

## ローカルでの確認

Pull request を作る前に、リポジトリのルートで次を実行してください。

```sh
make check
```

このコマンドは、次の検査をCIと同じ順序で実行します。

1. `gofmt` によるフォーマット確認
2. `go mod tidy -diff` による `go.mod` / `go.sum` の整合性確認
3. `go vet`、Staticcheck、actionlintによるリント
4. 全packageのビルド
5. 実行順をshuffleした全テスト
6. race detectorを有効にした全テスト

個別に実行する場合は、`make format-check`、`make mod-check`、`make lint`、`make build`、`make test`、`make test-race` を使えます。コードを整形するには `make fmt` を使います。

Staticcheckは、最低対応バージョンであるGo 1.25で動作する `v0.7.0` に固定しています。GitHub Actionsのworkflowはactionlint `v1.7.12` で検査します。CIでも `GOTOOLCHAIN=local` を設定し、別のGo toolchainへ暗黙に切り替わらないようにしています。

## CIとマージ条件

`.github/workflows/ci.yml` はpull request、`main`へのpush、merge queueで実行されます。必須チェック名は `CI required` です。workflowには意図的にpath filterを付けていません。変更内容にかかわらず必須チェックが作られるため、チェックが存在しないままマージ待ちになることを防げます。

`main`のrulesetでは次を必須にします。

- pull requestを経由する
- `CI required` が成功する
- pull request branchが最新の`main`を取り込んでいる
- review conversationがすべて解決されている
- force pushとbranch削除を許可しない

単独開発でも自分のpull requestを進められるよう、人間による承認数は0とします。フォーマット、リント、ビルド、通常テスト、race detector付きテストのどれか1つでも失敗すれば、`CI required` は成功せずマージできません。

## Codexによるレビュー

CodexのGitHub連携を使うと、ChatGPTのCodex利用枠でpull requestをレビューできます。初回だけCodexでGitHub repositoryを接続し、Code review設定で `uekann/ovl` のAutomatic reviewを有効にしてください。pull request作成時とpush後にレビューする設定を推奨します。

自動レビューが開始されない場合は、pull requestへ次のコメントを投稿して手動で依頼できます。

```text
@codex review
```

Codexはrootの`AGENTS.md`にある `## Code Review Rules` を読み、ovl固有の重大リスクを重点的に確認します。機械的に判定できる事項はCIへ置き、LLMにはOverleaf上のentity identity、同期時のデータ保護、コメント位置、credentialやprivate APIの扱いをレビューさせています。

この構成では、LLMの一時的な障害で全開発を止めないため、hard gateは再現可能な `CI required` と未解決conversationに限定しています。pull request templateのチェック項目に従い、Codexのレビューが返ってからマージしてください。

`openai/codex-action` をworkflow内で直接実行する方法もありますが、GitHub secretに `OPENAI_API_KEY` が必要で、ChatGPT Proとは別のAPI課金になります。このリポジトリでは、API keyを公開repositoryのpull request workflowへ持ち込まずに済むGitHub連携を標準とします。
