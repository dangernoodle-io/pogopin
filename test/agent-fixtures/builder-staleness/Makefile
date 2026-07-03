CC ?= gcc
CFLAGS ?= -O2 -Wall

.PHONY: all clean

all: app

# Deliberately incomplete dependency: main.o depends only on main.c, not on
# config.h. Editing config.h after a build leaves `make` believing main.o is
# up to date, so `make` reports "nothing to do" while the binary is stale.
main.o: main.c
	$(CC) $(CFLAGS) -c main.c -o main.o

app: main.o
	$(CC) $(CFLAGS) main.o -o app

clean:
	rm -f main.o app
