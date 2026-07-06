# Quill for VS Code

Syntax highlighting for [Quill](https://github.com/avmnu-sng/quill-template-engine)
templates. It contributes a `quill` language for files with the `.quill`
extension and a TextMate grammar (`source.quill`) that colors comments
(`{# ... #}`), interpolations (`{{ ... }}` with trim modifiers), `@`-statements
(`@if`, `@for`, `@block`, ...), string/number/bool/null literals, filters after
`|`, function and macro calls, and the word operators (`and`, `or`, `not`, `in`,
`is`, ...). Everything outside code delimiters is treated as literal template
text.

## Install locally

This directory is a self-contained VS Code extension. To try it without
publishing:

- **Symlink into your extensions folder**, then reload VS Code:

  ```
  ln -s "$(pwd)" ~/.vscode/extensions/quill-template-engine
  ```

- **Or package and install** with [`vsce`](https://github.com/microsoft/vscode-vsce):

  ```
  npm install -g @vscode/vsce
  vsce package
  code --install-extension quill-template-engine-0.1.0.vsix
  ```

Open any `.quill` file and Quill highlighting applies automatically.
