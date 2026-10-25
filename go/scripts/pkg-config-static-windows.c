/* Native wrapper: Windows Go cannot execute the POSIX shell wrapper. */
#include <process.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int main(int argc, char **argv) {
    const char *tool = getenv("DICAM_PKG_CONFIG");
    if (!tool || !*tool) { fputs("DICAM_PKG_CONFIG is not set\n", stderr); return 1; }
    const char **args = calloc((size_t)argc + 2, sizeof(*args));
    if (!args) return 1;
    char *program = malloc(strlen(tool) + 3);
    if (!program) { free(args); return 1; }
    sprintf(program, "\"%s\"", tool);
    args[0] = program;
    args[1] = "--static";
    int libraries = 0;
    for (int i = 1; i < argc; i++) {
        args[i + 1] = argv[i];
        if (!strcmp(argv[i], "--libs")) libraries = 1;
    }
    // Static archives can reference each other. u-config may emit avutil
    // before swscale; a GNU ld group resolves these references in any order.
    if (libraries) { fputs("-Wl,--start-group ", stdout); fflush(stdout); }
    intptr_t result = _spawnv(_P_WAIT, tool, args);
    if (libraries) fputs(" -Wl,--end-group\n", stdout);
    free(program);
    free(args);
    if (result == -1) { perror("pkg-config"); return 1; }
    return (int)result;
}
