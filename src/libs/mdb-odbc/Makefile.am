INCLUDES= \
	$(ALL_CFLAGS) \
	-I$(top_srcdir)/include \
	-DLOCALSTATEDIR=\""$(localstatedir)"\" \
	-DMDB_INTERNAL

LIBS= -lodbc

mdbdir=$(libdir)/bongomdb

mdb_LTLIBRARIES=libmdbodbc.la

libmdbodbc_la_SOURCES = \
	mdbodbc.c           \
	mdbodbcp.h

libmdbodbc_la_LDFLAGS = -module -avoid-version
libmdbodbc_la_LIBADD =  \
	../bongoutil/libbongoutil.la \
	../xpl/libbongoxpl.la
