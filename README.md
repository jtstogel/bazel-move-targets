# bazel-move-targets

Tool to move Bazel targets from a package into a subpackage.

This is a one-off tool built to split LLVM-libc's Bazel overlay BUILD file,
[`utils/bazel/llvm-project-overlay/libc/BUILD.bazel`](https://github.com/llvm/llvm-project/blob/f432af682a464d76e163e35699fd8cbf81da4cf1/utils/bazel/llvm-project-overlay/libc/BUILD.bazel).

The code here is pretty much entirely LLM-generated.

## Usage

```
go install github.com/jtstogel/bazel-move-targets@latest

bazel-move-targets plan  --workspace_root=DIR [selection flags] [--out=config.json]
bazel-move-targets apply --workspace_root=DIR --config=config.json [--dry_run]
bazel-move-targets move  --workspace_root=DIR [selection flags] [--dry_run]
```

Run `bazel-move-targets [command] --help` for more details about any command.
