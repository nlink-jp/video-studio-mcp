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

## Amendment (2026-09-22, v0.6.1): 呼び出し側が名指すファイルを読む前に判定する

`manifest_path` と、マニフェストが名指す画像・音声はワークスペース相対で `os.Root` 越しに扱うが、床には掛けて
いなかった。ワークスペースは `CheckBeneath` を通っても床の場所を含み得る（`.env`、このサーバーの設定ディレクトリ、
`~/.ssh` 内のリンクが同期フォルダを指すならその行き先）。そうしたファイルはマニフェストとして読まれて先頭が解析エラーに
出（`invalid character 'S'`）、ページとして名指せば受け付けられて ffmpeg に渡された。答えも、無いときの「not found」
「manifest_incomplete」と違った。slack-mcp-extender と chrome-pilot-mcp の独立レビューで見つかった「存在で答えが
変わる」型を、HOME を一時ディレクトリにしたテストで実測して見つけた（15 組中 9 組。仕掛けたリンクで外へ出る 6 組は
`os.Root` が存在に関係なく拒んでいた）。

- master ツールは `manifest_path` を、続いて各ページの画像と音声を `refused`（internal/tools/master.go）で判定する
  —— pathguard の Local 方針とこのサーバー自身のディレクトリ。マニフェストは `ws.ReadFile` の前に、ページは `Build`
  が探す前に（非同期でも呼び出しの時点で）。pathguard がパス上のリンクを自分で辿るので、置き場所を別に求める必要は無い。
- `TestExistenceIsNotRevealed` は、同じパスをファイルがある状態と消した状態で、マニフェスト・画像・音声として
  `master` を呼び、答え全体を比べ、中身が出ないことを確かめる。3 つの変異（床を外す・マニフェストの判定を外す・
  ページの判定を外す）はすべてアサーションで落ちた。
- 既知の限界（いずれも pathguard 側。次のリリースに向けて記録）:
  - `work_dir` は pathguard/workdir が組織 ADR-022 §4 の順序（not found が denied より先）で検証するので、資格情報の
    ディレクトリを指す `work_dir` は、存在するかどうかで答えが変わる。
  - 非 ASCII 名のリンク先を別の Unicode 正規化で綴ると、同一性で拒むのはそれが存在するときだけになる（pathguard は
    正規化しない）。別の場所に作った資格情報ファイルへのハードリンクも同じ。ハードリンクを作れる者はすでにそのファイルに
    届いている。

## References

- 組織 ADR-021（ファイル渡し MCP サーバーの work dir 契約）
- ADR-0008（work dir 契約）: 検証の閉じた一覧 —— その実装をここで置き換える
- nlink-jp/pathguard の RFP（`docs/ja/pathguard-rfp.ja.md`）
