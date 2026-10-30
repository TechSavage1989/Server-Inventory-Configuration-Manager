SERVER INVENTORY 3.2.10 - SYNTHETIC STARTUP DIAGNOSTIC

This is a diagnostic, not an app update. It tests seven original report helper
functions with made-up empty, circular, and nested values. It does NOT run the
inventory collector, query server roles, export configuration, connect to any
network, install anything, request administrator rights, or change security settings.

Use only if you choose to run the diagnostic on the affected Server 2019 computer.
1. Extract this ZIP into a NEW writable folder.
2. Double-click Run-Diagnostic.cmd normally (do not Run as administrator).
3. Wait up to 60 seconds. A Startup-Diagnostic-*.txt file appears in that folder.
4. Send that text file, or its last BEGIN/PASS lines and child exit code.

If Windows or PowerShell blocks script execution, STOP and report that message.
Do not disable protection, alter execution policy, or install another PowerShell.

The supervisor verifies the helper script checksum before launching it, limits
that child to 60 seconds, and terminates only its own child if it times out.
A stack overflow can close the child process; the supervisor retains its output.
The log contains OS/PowerShell/CLR version numbers, test checkpoints, exit code,
and errors. It contains no collected inventory or credentials. Execution-policy
errors can include this local diagnostic folder path; redact that path if desired.
Nothing is uploaded automatically. Delete the extracted folder when finished.

Provenance: helper definitions and startup self-test statements copied verbatim
from the published 3.2.10 collector with source SHA256:
B072C3CF15F1CEF14DF397718A311A753CFAE0A7B12EBF4380FBED4A2D01C091
Only diagnostic metadata and before/after checkpoints are added.
The complete inventory collector and wrapper are NOT included.
No production application or release is changed.
