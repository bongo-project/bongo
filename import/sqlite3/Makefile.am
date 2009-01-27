
AM_CFLAGS = @THREADSAFE_FLAGS@ @DYNAMIC_EXTENSION_FLAGS@

lib_LTLIBRARIES = libbongosqlite3.la
libbongosqlite3_la_SOURCES = sqlite3.c
libbongosqlite3_la_LDFLAGS = -no-undefined -version-info 8:6:8

bin_PROGRAMS = bongosqlite3
bongosqlite3_SOURCES = shell.c sqlite3.h
bongosqlite3_LDADD = $(top_builddir)/libbongosqlite3.la @READLINE_LIBS@
bongosqlite3_DEPENDENCIES = $(top_builddir)/libbongosqlite3.la

include_HEADERS = sqlite3.h sqlite3ext.h

