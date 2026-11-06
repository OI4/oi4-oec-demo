# The logger demo D4 (ADR-0028): a C program on the OEC C ABI (oec-ffi,
# ADR-0021), linked statically with liboec_ffi.a.
#
#   make                 build target/oec-logger
#   make test            build and run the unit tests (no SDK needed)
#   make SANITIZE=1 ...  the same with AddressSanitizer and UBSan in
#                        target/asan/
#   make gcc-check GCC=gcc-15
#                        compile every source with GCC -O2 (its warnings,
#                        e.g. -Wformat-truncation, are those of the Linux
#                        image build; C-084), no output
#
# OEC_INCLUDE is the directory of oec.h, OEC_LIB the liboec_ffi.a; the
# defaults are the release build of oec/sdk/rust (cargo build --release
# -p oec-ffi).

OEC_INCLUDE ?= ../../sdk/rust/oec-ffi/include
OEC_LIB ?= ../../sdk/rust/target/release/liboec_ffi.a
CC ?= cc
OUT = target

WARN = -std=c17 -Wall -Wextra -Wpedantic -Werror -Wshadow -Wstrict-prototypes \
	-Wmissing-prototypes -Wformat=2 -Wvla
# POSIX for clock_gettime, open_memstream, gmtime_r and pthreads.
DEFS = -D_POSIX_C_SOURCE=200809L -D_DEFAULT_SOURCE
OPT = -O2 -g

ifeq ($(SANITIZE),1)
OUT = target/asan
OPT = -O1 -g -fsanitize=address,undefined -fno-omit-frame-pointer -fno-sanitize-recover=all
endif

UNAME := $(shell uname -s)
ifeq ($(UNAME),Darwin)
NATIVE = -liconv -lm # libSystem is linked by cc
else
NATIVE = -lgcc_s -lutil -lrt -lpthread -lm -ldl -lc
endif

CORE = src/json.c src/core.c src/history.c src/http.c src/ui.c src/ui_assets.c
HEADERS = src/json.h src/core.h src/history.h src/http.h src/ui.h

.PHONY: all test clean gcc-check
all: $(OUT)/oec-logger

$(OUT)/oec-logger: src/main.c $(CORE) $(HEADERS) $(OEC_INCLUDE)/oec.h $(OEC_LIB)
	@mkdir -p $(OUT)
	$(CC) $(WARN) $(DEFS) $(OPT) -I$(OEC_INCLUDE) -o $@ src/main.c $(CORE) $(OEC_LIB) \
		-lpthread $(NATIVE)

$(OUT)/unit: test/unit.c $(CORE) $(HEADERS)
	@mkdir -p $(OUT)
	$(CC) $(WARN) $(DEFS) $(OPT) -o $@ test/unit.c $(CORE) -lpthread -lm

test: $(OUT)/unit
	./$(OUT)/unit

gcc-check:
	@for f in src/main.c $(CORE) test/unit.c; do \
		$(GCC) $(WARN) $(DEFS) -O2 -I$(OEC_INCLUDE) -c $$f -o /dev/null || exit 1; \
	done

clean:
	rm -rf target
