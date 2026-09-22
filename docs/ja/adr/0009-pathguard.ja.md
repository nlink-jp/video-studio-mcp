# ADR-0009: パスの判定は nlink-jp/pathguard に任せる — 写しを持たない

- Status: Accepted
- Date: 2026-09-22

## Context

ADR-0008 以来、`work_dir` の検証は `internal/workdir` にあった。voice-scribe（組織 ADR-021 の参照実装）
からの写しで、同じ写しがほかの 7 サーバーにもあった。どの写しも場所を**名前で**比べていた。APFS は既定で
大文字小文字を区別しないので、`~/.SSH`、`/USR/local` のような綴りが同じ場所を指しながら検査を通った。
ホームディレクトリが分からないときは、資格情報の位置の検査がすべてを通した。

組織はこの判定を 1 つのモジュールにまとめた（`nlink-jp/pathguard`、lib-series）。場所をファイルの実体と、
ディスクと同じやり方で同一視した名前の両方で比べ、まだ存在しない場所もその親の実体で捕まえる。一覧は
gem-agent・lagent と同じものを 1 つ持つ。

## Decision

- `github.com/nlink-jp/pathguard` v0.1.0 を依存に加える。この org の外のコードは入らない。
- `internal/workdir` は**薄いアダプタ**にする。持つのは次だけ:
  - リクエストの `_meta` を文脈から取り出して `pathguard/workdir` の `Resolve` に渡すこと、
  - その `*workdir.Error` を `toolerr` の同じ code・message・details に移すこと、
  - `NewResolver(serverDirs...)` —— このサーバー自身のディレクトリ（今は設定ディレクトリ
    `~/.config/video-studio-mcp` だけ）を守る場所（`pathguard.ServerDir`）として渡し、`work_dir_required`
    の 1 文を `RequiredHint` で添える。空のパスは何も守らないのではなく、すべての呼び出しを拒ませる
    （設定ディレクトリが決まらないのはホームが分からないときで、そのときは pathguard もすべてを拒む）。
- `Sensitive` は持たない。このサーバーのファイル引数（`manifest_path` と、マニフェストの各ページの画像・音声）はすべて
  ワークスペース相対で、`os.Root` が封じ込める。`work_dir` の外のパスを読むことが無い。
- 呼び出し箇所（`Resolve`・`Validate`）は変えない。変わるのは組み立ての 1 行（`cmd/tools_wiring.go` の
  `workDirResolver`）と、ゼロ値で組み立てていたテストだけである。
- 判定そのもののテストは pathguard にある。ここに残すのはアダプタのテスト（`_meta` の取り出し、エラーの写し、
  守る場所、ゼロ値が拒むこと）と、既存の契約テスト・配線のテストである。

## Consequences

`work_dir` の検査が変わる（CHANGELOG に書く）:

- **新たに拒む**: ランタイムと同じ一覧のうち、自分のホームにある本物の場所（`~/.kube`、`~/.config/gh`、
  `~/.azure`、`~/.terraform.d`、`~/.gemini`、`~/.config/mcp-bridge`、`~/.netrc`、`~/.npmrc`、`~/.pypirc`、
  `~/.git-credentials`、`~/.vault-token`、`~/.docker/config.json`、`~/.claude.json`、`~/.bash_history`、
  `~/.zsh_history`）。床のどの場所についても、大文字小文字の違い・リンク・ファームリンクなど、あらゆる綴り。
  それらのディレクトリの直下にあるリンクの指す先。`$HOME` がアカウントのホームと違うときは、両方を守る。
  Linux の `/etc`。
- **ホームが分からなければ、どの `work_dir` も拒む**。以前は通していた。
- `work_dir_denied` の `details` に `reason` が加わる。
- 1 回の検査は約 2 ms（pathguard の実測）。レンダーの時間に比べて無視できる。

写しを持たないので、判定の修正は pathguard のリリースと、ここでの依存の更新 1 行になる。

## Amendment (2026-09-22): 実際に使うディレクトリも判定する

`work_dir` だけを検査していたので、`work_dir=~/.config` と `workspace_id=gh` でワークスペースが `~/.config/gh` になり、
動画とその素材がそこへ書かれた。ADR-0008 の頃からの穴で、image-forge の独立レビューで見つかった。
`workspace.NewManager(check)` は判定を必須の引数として受け取り、`EnsureUnder` は `<work_dir>/<workspace_id>` を
作る前・使う前に `workdir.Resolver.CheckBeneath`（pathguard v0.2.0）で判定する。配線は `newToolDeps`。判定の無い
Manager はすべてのワークスペースを拒む。pathguard v0.2.0 は NUL バイトを含むパスも拒む。

## References

- 組織 ADR-021（ファイル渡し MCP サーバーの work dir 契約）
- ADR-0008（work dir 契約）: 検証の閉じた一覧 —— その実装をここで置き換える
- nlink-jp/pathguard の RFP（`docs/ja/pathguard-rfp.ja.md`）
