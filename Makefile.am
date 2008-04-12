#
# This is a non-recursive (except for imports) automake file.
#
# The style was inspired by Robert Collins, as mentioned here:
#
#     http://sources.redhat.com/ml/automake/2001-08/msg00061.html
#
# And the original Recursive Make Considered Harmful:
#
#     http://www.pcug.org.au/~millerp/rmch/recu-make-cons-harm.html
#

AUTOMAKE_OPTIONS = subdir-objects
ACLOCAL_AMFLAGS = -I m4 -I macros

NULL =

bin_PROGRAMS =
sbin_PROGRAMS =
noinst_PROGRAMS =
check_PROGRAMS =

sbin_SCRIPTS =

dist_sbin_SCRIPTS =
dist_noinst_SCRIPTS =
dist_pkglibexec_SCRIPTS =

lib_LTLIBRARIES =

nobase_dist_pkgdata_DATA =
dist_noinst_DATA = 
noinst_DATA =

noinst_HEADERS =

BUILT_SOURCES =
CLEANFILES =

TESTS =

AM_CPPFLAGS = $(ALL_CFLAGS)			  \
	$(NO_POINTER_SIGN_WARNINGS)		  \
        $(PTHREAD_CFLAGS)			  \
	-I$(top_srcdir)/include $(GNUTLS_CFLAGS)

EXTRA_DIST =					\
	config.rpath				\
	bongo.pc.in				\
	HACKING					\
	include/bongo-buildinfo.h		\
	init/bongo.init.fc4.in			\
	init/bongo.init.sles9.in		\
	init/bongo.init.suse10.in		\
	init/bongo.xml.solaris-smf		\
	init/http-bongo.xml.solaris-smf		\
	init/http-bongo.solaris-smf.in		\
	init/svc-bongo.solaris-smf.in		\
	mkinstalldirs

localedir = $(datadir)/locale
DEFS = -DLOCALEDIR=\"$(localedir)\" @DEFS@

pkgconfigdir = $(libdir)/pkgconfig
pkgconfig_DATA = bongo.pc

pkglibexecdir = $(libexecdir)/$(PACKAGE)

install-data-hook::
	$(mkinstalldirs) $(DESTDIR)$(XPL_DEFAULT_CACHE_DIR)

SUBDIRS =			\
	$(IMPORT_SUBDIRS)	\
	import/log4c		\
	po

doxygen/html:
	if test "$(DOXYGEN)" != NO_DOXYGEN; then \
	  $(DOXYGEN) ./doxygen/doxygen.conf; \
	else \
	  echo "No doxygen available"; \
	fi

doxygen/diagrams:
	mkdir -p doxygen/diagrams
	find src/ -name \*.dot -printf "-o doxygen/diagrams/%f %h/%f" | xargs -l1 dot -Tpng -Nfontname="Bitstream Vera Sans" -Nfontsize=12
	rename .dot .png doxygen/diagrams/* 

docs:	doxygen/html doxygen/diagrams

clean:	clean-recursive clean-docs

clean-docs:
	rm -rf ./doxygen/html ./doxygen/latex ./doxygen/diagrams

#	disabled agents
#	src/agents/mailprox			
#	src/agents/rules			
#	src/agents/calcmd

include_subdirs =				\
	include					\
	src/libs/xpl				\
	src/libs/xpl/tests			\
	src/libs/memmgr				\
	src/libs/bongoutil			\
	src/libs/bongoutil/tests		\
	src/libs/connio				\
	src/libs/connio/tests			\
	src/libs/json				\
	src/libs/logger				\
	src/libs/streamio			\
	src/libs/streamio/codec			\
	src/libs/cal				\
	src/libs/cal/tests			\
	src/libs/nmap				\
	src/libs/msgapi				\
	src/libs/calcmd				\
	src/libs/calcmd/tests			\
	src/libs/python				\
	src/libs/python/libbongo		\
	src/agents/alarm			\
	src/agents/antispam			\
	src/agents/avirus			\
	src/agents/collector			\
	src/agents/generic			\
	src/agents/imap				\
	src/agents/imap/tests			\
	src/agents/itip				\
	src/agents/pop				\
	src/agents/queue			\
	src/agents/smtp				\
	src/agents/store			\
	src/agents/store/tests			\
	src/apps/backup				\
	src/apps/config				\
	src/apps/manager			\
	src/apps/sendmail			\
	src/apps/queuetool			\
	src/apps/storetool			\
	src/apps/storetool/demo			\
	src/apps/testtool			\
	src/www					\
	zoneinfo				\
	man						\
	po

#	src/apps/migrate

DIST_SUBDIRS = $(SUBDIRS) $(include_subdirs)

include include/Bongo.rules
include src/libs/xpl/Bongo.rules
include src/libs/xpl/tests/Bongo.rules
include src/libs/memmgr/Bongo.rules
include src/libs/bongoutil/Bongo.rules
include src/libs/bongoutil/tests/Bongo.rules
include src/libs/connio/Bongo.rules
include src/libs/connio/tests/Bongo.rules
include src/libs/json/Bongo.rules
include src/libs/logger/Bongo.rules
include src/libs/streamio/Bongo.rules
include src/libs/streamio/codec/Bongo.rules
include src/libs/cal/Bongo.rules
include src/libs/cal/tests/Bongo.rules
include src/libs/nmap/Bongo.rules
include src/libs/msgapi/Bongo.rules
include src/libs/calcmd/Bongo.rules
include src/libs/calcmd/tests/Bongo.rules
include src/libs/python/Bongo.rules
include src/libs/python/libbongo/Bongo.rules
include src/agents/alarm/Bongo.rules
include src/agents/antispam/Bongo.rules
include src/agents/avirus/Bongo.rules
#include src/agents/calcmd/Bongo.rules
include src/agents/collector/Bongo.rules
include src/agents/generic/Bongo.rules
include src/agents/imap/Bongo.rules
include src/agents/imap/tests/Bongo.rules
include src/agents/itip/Bongo.rules
#include src/agents/mailprox/Bongo.rules
include src/agents/pop/Bongo.rules
include src/agents/queue/Bongo.rules
#include src/agents/rules/Bongo.rules
include src/agents/smtp/Bongo.rules
include src/agents/store/Bongo.rules
include src/agents/store/tests/Bongo.rules
include src/apps/backup/Bongo.rules
include src/apps/config/Bongo.rules
include src/apps/manager/Bongo.rules
include src/apps/sendmail/Bongo.rules
# include src/apps/migrate/Bongo.rules
include src/apps/queuetool/Bongo.rules
include src/apps/storetool/Bongo.rules
include src/apps/storetool/demo/Bongo.rules
include src/apps/testtool/Bongo.rules
include src/www/Bongo.rules
include zoneinfo/Bongo.rules
include man/Bongo.rules
include po/Bongo.rules

update-makefiles:
	@for dir in $(include_subdirs) ; do \
		echo " cp $(top_srcdir)/Makefile.am.subdir $(top_srcdir)/$$dir/Makefile.am" ; \
		cp "$(top_srcdir)/Makefile.am.subdir" "$(top_srcdir)/$$dir/Makefile.am" || exit 1 ; \
		if test "$$dir" == "po" ; then \
			echo "" >> "$(top_srcdir)/$$dir/Makefile.am" || exit 1 ; \
			echo "distclean:" >> "$(top_srcdir)/$$dir/Makefile.am" || exit 1 ; \
		fi ; \
	done
