INCLUDES = \
	-I$(srcdir)/../ \
	-DLOG4C_RCPATH="\"$(sysconfdir)/bongo\""

lib_LTLIBRARIES  = libbongolog4c.la

libbongolog4c_la_SOURCES = \
	rc.c \
	init.c \
	appender_type_stream.c \
	appender_type_stream2.c \
	appender_type_syslog.c \
	appender_type_mmap.c \
	appender_type_audit.c \
	layout_type_basic.c \
	layout_type_dated.c \
	version.c \
	logging_event.c \
	priority.c \
	appender.c \
	layout.c \
	category.c \
	log4c.c

libbongolog4c_la_LIBADD = \
	../sd/libbongolog4c_sd.la

LIBS = \
	$(LIBLOGEVENT)

bongolog4cincludedir=$(includedir)/bongo/log4c

bongolog4cinclude_HEADERS = \
	rc.h \
	init.h \
	defs.h \
	version.h \
	location_info.h \
	logging_event.h \
	priority.h \
	layout_type_basic.h \
	layout_type_dated.h \
	layout.h \
	appender_type_stream.h \
	appender_type_stream2.h \
	appender_type_syslog.h \
	appender_type_mmap.h \
	appender_type_audit.h \
	appender.h \
	category.h \
	buffer.h
