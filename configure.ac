dnl -*- mode: m4 -*-
AC_PREREQ(2.52)

AC_INIT(src/libs/msgapi/msgapi.c)

AM_INIT_AUTOMAKE(bongo, 0.1.0)

# this space is here to trick autogen.sh
 AM_GNU_GETTEXT([external])
AM_CONDITIONAL(USE_NLS,test "$USE_NLS" = "yes")

AC_CONFIG_HEADERS(config.h)
AX_PREFIX_CONFIG_H([include/bongo-config.h], _BONGO, [config.h])

# Honor aclocal flags
ACLOCAL="$ACLOCAL $ACLOCAL_FLAGS"

GETTEXT_PACKAGE=bongo
AC_SUBST(GETTEXT_PACKAGE)
AC_DEFINE_UNQUOTED(GETTEXT_PACKAGE,"$GETTEXT_PACKAGE",[The name of the gettext domain])

 ## must come before we use the $USE_MAINTAINER_MODE variable later
AM_MAINTAINER_MODE

AC_PROG_CC
AM_PROG_CC_C_O
AC_PROG_CXX
AC_ISC_POSIX
AC_HEADER_STDC
AC_C_BIGENDIAN

# Check taken from glib
AC_MSG_CHECKING(for the suffix of shared libraries)
case "$host_os" in
  hpux9* | hpux10* | hpux11*)  # taken from ltconfig
    glib_gmodule_suffix='sl'
    ;;
  cygwin* | mingw*)
    glib_gmodule_suffix='dll'
    ;;
  aix*)
    glib_gmodule_suffix='a'
    ;;
  *)
    glib_gmodule_suffix='so'
    ;;
esac
AC_MSG_RESULT(.$glib_gmodule_suffix)
SHARED_POSTFIX=".$glib_gmodule_suffix"
AC_SUBST(SHARED_POSTFIX)

#### gcc warning flags

NO_POINTER_SIGN_WARNINGS=

if test "x$GCC" = "xyes"; then
  changequote(,)dnl
  case " $CFLAGS " in
  *[\ \	]-Wall[\ \	]*) ;;
  *) CFLAGS="$CFLAGS -Wall" ;;
  esac

  case " $CFLAGS " in
  *[\ \	]-Wchar-subscripts[\ \	]*) ;;
  *) CFLAGS="$CFLAGS -Wchar-subscripts" ;;
  esac

  case " $CFLAGS " in
  *[\ \	]-Wmissing-declarations[\ \	]*) ;;
  *) CFLAGS="$CFLAGS -Wmissing-declarations" ;;
  esac

  case " $CFLAGS " in
  *[\ \	]-Wmissing-prototypes[\ \	]*) ;;
  *) CFLAGS="$CFLAGS -Wmissing-prototypes" ;;
  esac

  case " $CFLAGS " in
  *[\ \	]-Wnested-externs[\ \	]*) ;;
  *) CFLAGS="$CFLAGS -Wnested-externs" ;;
  esac

  case " $CFLAGS " in
  *[\ \	]-Wpointer-arith[\ \	]*) ;;
  *) CFLAGS="$CFLAGS -Wpointer-arith" ;;
  esac

  case " $CFLAGS " in
  *[\ \	]-Wcast-align[\ \	]*) ;;
  *) CFLAGS="$CFLAGS -Wcast-align" ;;
  esac

  case " $CFLAGS " in
  *[\ \	]-Wsign-compare[\ \	]*) ;;
  *) CFLAGS="$CFLAGS -Wsign-compare" ;;
  esac

  if test "x$enable_ansi" = "xyes"; then
    case " $CFLAGS " in
    *[\ \	]-ansi[\ \	]*) ;;
    *) CFLAGS="$CFLAGS -ansi" ;;
    esac

    case " $CFLAGS " in
    *[\ \	]-D_POSIX_C_SOURCE*) ;;
    *) CFLAGS="$CFLAGS -D_POSIX_C_SOURCE=199309L" ;;
    esac

    case " $CFLAGS " in
    *[\ \	]-D_BSD_SOURCE[\ \	]*) ;;
    *) CFLAGS="$CFLAGS -D_BSD_SOURCE" ;;
    esac

    case " $CFLAGS " in
    *[\ \	]-pedantic[\ \	]*) ;;
    *) CFLAGS="$CFLAGS -pedantic" ;;
    esac
  fi
  if test x$enable_gcov = xyes; then
    case " $CFLAGS " in
    *[\ \	]-fprofile-arcs[\ \	]*) ;;
    *) CFLAGS="$CFLAGS -fprofile-arcs" ;;
    esac
    case " $CFLAGS " in
    *[\ \	]-ftest-coverage[\ \	]*) ;;
    *) CFLAGS="$CFLAGS -ftest-coverage" ;;
    esac

    ## remove optimization
    CFLAGS=`echo "$CFLAGS" | sed -e 's/-O[0-9]*//g'`
  fi

  if test -z "$ac_cv_prog_CC"; then
    our_gcc="$CC"
  else
    our_gcc="$ac_cv_prog_CC"
  fi
  case `$our_gcc --version | sed -e 's,\..*,.,' -e q` in
    *4.) NO_POINTER_SIGN_WARNINGS=-Wno-pointer-sign ;;
    *)   NO_POINTER_SIGN_WARNINGS="" ;;
  esac    

  changequote([,])dnl
fi

AC_SUBST(NO_POINTER_SIGN_WARNINGS)

AM_PROG_LIBTOOL

#
# Make libtool use --silent when --silent is passed to make
#
changequote(,)dnl
LIBTOOL="${LIBTOOL} \$(shell echo \"\$(MFLAGS)\" | awk '/^[^ ]*s/ { print \"--silent\" }')"
changequote([,])dnl

AC_DEFINE_UNQUOTED(LINUX, "1", [Badly named building-under-autoconf variable])

#####3 Directory management - from dbus' configure.in

AS_AC_EXPAND(LOCALSTATEDIR, $localstatedir)
AS_AC_EXPAND(SYSCONFDIR, $sysconfdir)
AS_AC_EXPAND(DATADIR, "$prefix/share")
AS_AC_EXPAND(BINDIR, $bindir)
AS_AC_EXPAND(SBINDIR, $sbindir)
AS_AC_EXPAND(LIBDIR, $libdir)
AS_AC_EXPAND(PKGLIBEXECDIR, "$libexecdir/$PACKAGE")

### Paths

XPL_DEFAULT_DATA_DIR=${DATADIR}/bongo
AC_SUBST(XPL_DEFAULT_DATA_DIR)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_DATA_DIR, "$XPL_DEFAULT_DATA_DIR", [Default data directory])

XPL_DEFAULT_STATE_DIR=${LOCALSTATEDIR}/bongo
AC_SUBST(XPL_DEFAULT_STATE_DIR)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_STATE_DIR, "$XPL_DEFAULT_STATE_DIR", [Default state directory])

XPL_DEFAULT_WORK_DIR=${XPL_DEFAULT_STATE_DIR}/work
AC_SUBST(XPL_DEFAULT_WORK_DIR)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_WORK_DIR, "$XPL_DEFAULT_WORK_DIR", [Default work directory])

XPL_DEFAULT_DBF_DIR=${XPL_DEFAULT_STATE_DIR}/dbf
AC_SUBST(XPL_DEFAULT_DBF_DIR)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_DBF_DIR, "$XPL_DEFAULT_DBF_DIR", [Default dbf directory])

XPL_DEFAULT_CACHE_DIR=${XPL_DEFAULT_STATE_DIR}/cache
AC_SUBST(XPL_DEFAULT_CACHE_DIR)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_CACHE_DIR, "$XPL_DEFAULT_CACHE_DIR", [Default cache directory])

XPL_DEFAULT_BIN_DIR=${SBINDIR}
AC_SUBST(XPL_DEFAULT_BIN_DIR)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_BIN_DIR, "$XPL_DEFAULT_BIN_DIR", [Default binary directory])

XPL_DEFAULT_LIB_DIR=${LIBDIR}
AC_SUBST(XPL_DEFAULT_LIB_DIR)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_LIB_DIR, "$XPL_DEFAULT_LIB_DIR", [Default library directory])

XPL_DEFAULT_CONF_DIR=${SYSCONFDIR}/bongo
AC_SUBST(XPL_DEFAULT_CONF_DIR)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_CONF_DIR, "$XPL_DEFAULT_CONF_DIR", [Default config directory])

XPL_DEFAULT_NLS_DIR=${DATADIR}/bongo/nls
AC_SUBST(XPL_DEFAULT_NLS_DIR)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_NLS_DIR, "$XPL_DEFAULT_NLS_DIR", [Default nls directory])

XPL_DEFAULT_SCMS_DIR=${XPL_DEFAULT_STATE_DIR}/scms
AC_SUBST(XPL_DEFAULT_SCMS_DIR)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_SCMS_DIR, "$XPL_DEFAULT_SCMS_DIR", [Default scms directory])

XPL_DEFAULT_SPOOL_DIR=${XPL_DEFAULT_STATE_DIR}/spool
AC_SUBST(XPL_DEFAULT_SPOOL_DIR)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_SPOOL_DIR, "$XPL_DEFAULT_SPOOL_DIR", [Default spool directory])

XPL_DEFAULT_STORE_SYSTEM_DIR=${XPL_DEFAULT_STATE_DIR}/system
AC_SUBST(XPL_DEFAULT_STORE_SYSTEM_DIR)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_STORE_SYSTEM_DIR, "$XPL_DEFAULT_STORE_SYSTEM_DIR", [Default store system directory])

XPL_DEFAULT_MAIL_DIR=${XPL_DEFAULT_STATE_DIR}/users
AC_SUBST(XPL_DEFAULT_MAIL_DIR)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_MAIL_DIR, "$XPL_DEFAULT_MAIL_DIR", [Default mail directory])

XPL_DEFAULT_CERT_PATH=${XPL_DEFAULT_STATE_DIR}/dbf/osslcert.pem
AC_SUBST(XPL_DEFAULT_CERT_PATH)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_CERT_PATH, "$XPL_DEFAULT_CERT_PATH", [Default cert path])

XPL_DEFAULT_RSAPARAMS_PATH=${XPL_DEFAULT_STATE_DIR}/dbf/rsaparams.pem
AC_SUBST(XPL_DEFAULT_RSAPARAMS_PATH)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_RSAPARAMS_PATH, "$XPL_DEFAULT_RSAPARAMS_PATH", [Default path for RSA parameters])

XPL_DEFAULT_DHPARAMS_PATH=${XPL_DEFAULT_STATE_DIR}/dbf/dhparams.pem
AC_SUBST(XPL_DEFAULT_DHPARAMS_PATH)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_DHPARAMS_PATH, "$XPL_DEFAULT_DHPARAMS_PATH", [Default path for DH parameters])

XPL_DEFAULT_RANDSEED_PATH=${XPL_DEFAULT_STATE_DIR}/dbf/randomseed
AC_SUBST(XPL_DEFAULT_RANDSEED_PATH)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_RANDSEED_PATH, "$XPL_DEFAULT_RANDSEED_PATH", [Default path for seed to RNG])

XPL_DEFAULT_KEY_PATH=${XPL_DEFAULT_STATE_DIR}/dbf/osslpriv.pem
AC_SUBST(XPL_DEFAULT_KEY_PATH)
AC_DEFINE_UNQUOTED(XPL_DEFAULT_KEY_PATH, "$XPL_DEFAULT_KEY_PATH", [Default key path])

SAVE_LIBS=$LIBS
LIBS=

AC_CHECK_LIB(resolv, res_init, 
	[],
	[
		AC_CHECK_LIB(resolv, __res_init)
	]
	)

RESOLV_LIBS=$LIBS
LIBS=$SAVE_LIBS

AC_SUBST(RESOLV_LIBS)

have_ldap="no"
AC_CHECK_HEADER(ldap.h,[
	AC_CHECK_LIB(ldap, ldap_init,[
		LDAP_LIBS="-lldap"
		have_ldap="yes"
	])
])
AM_CONDITIONAL(HAVE_LDAP,test "$have_ldap" = "yes")
AC_SUBST(LDAP_LIBS)

have_kstat="no"
AC_CHECK_LIB(kstat, kstat_open,[
	AC_CHECK_HEADERS(kstat.h, [have_kstat="yes"])
])

if test "$have_kstat" = "yes"; then
	KSTAT_LIBS="-lkstat"
fi
AC_SUBST(KSTAT_LIBS)

AM_PROG_LEX

if test "$LEX" = "lex" -a -z "`which $LEX 2>/dev/null`"; then
	AC_MSG_ERROR([Could not locate a suitable lexical generator. Install flex.])
fi

AC_PROG_YACC

if test "$YACC" = "yacc" -a -z "`which $YACC 2>/dev/null`"; then
	AC_MSG_ERROR([Could not locate a suitable parser generator. Install bison.])
fi

AC_CHECK_FUNCS(strcasecmp strncasecmp stricmp strnicmp gettimeofday vsnprintf _vsnprintf consoleprintf ungetcharacter RingTheBell dlopen dlsym _commit fsync FEFlushWrite dos2calendar _ConvertDOSTimeToCalendar delay statfs statvfs getpwnam_r snprintf _snprintf gmtime_r localtime_r usleep nanosleep)

AC_CHECK_HEADERS(syslog.h sys/param.h sys/sys/mount.h semaphore.h sys/socket.h netinet/in.h resolv.h sys/sysinfo.h)

AC_CHECK_HEADERS(sys/mount.h sys/statvfs.h sys/vfs.h alloca.h)

AC_CHECK_DECLS([ns_c_in],,,[#include <arpa/nameser.h>])

ALL_CFLAGS='-I$(top_srcdir)/import/log4c -I$(top_srcdir)/import/libical/src -I$(top_builddir)/import/libical/src -I$(top_srcdir)/import/libical/src/libical -I$(top_builddir)/import/libical/src/libical'
ALL_LIBS=

AC_SUBST(ALL_CFLAGS)
AC_SUBST(ALL_LIBS)

DL_LIBS=
AC_CHECK_LIB(c, dlopen, DL_LIBS="", [AC_CHECK_LIB(dl, dlopen, DL_LIBS="-ldl")])
AC_SUBST(DL_LIBS)

AC_PATH_PROG(PKG_CONFIG, pkg-config, no)
if test "x$PKG_CONFIG" = "xno"; then
    AC_MSG_ERROR([Bongo requires pkg-config to be installed])
fi

have_check=no
AM_PATH_CHECK(0.9.0,
    [
        have_check=yes
        AC_DEFINE(BONGO_HAVE_CHECK, [], [Controls how tests that depend on check compile.])
    ],
    [
        AC_MSG_WARN([The check C unit testing framework is not installed,])
        AC_MSG_WARN([or is not sufficiently up to date.])
        AC_MSG_WARN([Tests relying on it in the make check target will not run.])
    ]
)
AM_CONDITIONAL(HAVE_CHECK, test x$have_check = xyes)

dnl The "manual" configuration is because a couple of old distros don't have
dnl the GNUTLS m4 thing. Centos 4, RHEL 4, Mandriva 2006(?)
PKG_CHECK_MODULES(GNUTLS, gnutls,
	echo Using pkg-config GNUTLS configuration,
	echo Trying to configure GNUTLS manually
	GNUTLS_LIBS="-lgnutls"
	GNUTLS_CFLAGS=""
)

AM_PATH_LIBGCRYPT()

AM_PATH_PYTHON(2.3)
PYTHON_INST_PREFIX=`$PYTHON -c 'import sys; print sys.prefix'`
PYTHON_CPPFLAGS="-I$PYTHON_INST_PREFIX/include/python$PYTHON_VERSION"
case "$host" in
    *darwin*)
	PYTHON_LIBS="-framework Python"
	;;
    *)	
	# this is the correct way to find the python libdir, but
	# people should just set LDFLAGS or whatever instead:
#	PYTHON_LIBS=`$PYTHON -c 'import sys
#try:
#    print sys.lib
#except:
#    print "lib"
#'`
#	PYTHON_LIBS="-L$PYTHON_INST_PREFIX/$PYTHON_LIBS -lpython$PYTHON_VERSION"
	PYTHON_LIBS="-lpython$PYTHON_VERSION"
	;;
esac

LIBS_save="$LIBS"
CPPFLAGS_save="$CPPFLAGS"

LIBS="$LIBS $PYTHON_LIBS"
CPPFLAGS="$CPPFLAGS $PYTHON_CPPFLAGS"

have_python="no"
AC_CHECK_FUNC(PyArg_Parse, [
	AC_CHECK_HEADERS(Python.h, [have_python="yes"])
])
if test x$have_python != xyes ; then
	AC_MSG_ERROR([Dragonfly requires python-devel, or python built with shared library support (--enable-shared)])
fi

LIBS="$LIBS_save"
CPPFLAGS="$CPPFLAGS_save"

AC_SUBST(CFLAGS_save)

AC_SUBST(PYTHON_LIBS)
AC_SUBST(PYTHON_CPPFLAGS)

AS_AC_EXPAND(PYTHONDIR, $pythondir)

# Check for openldap's slapd
AC_ARG_WITH([slapd],
	AC_HELP_STRING([--with-slapd=FILE],
		[Path to slapd binary]),
	[DEFAULT_SLAPD_PATH="$with_slapd"],
	[AC_PATH_PROG(DEFAULT_SLAPD_PATH,[slapd],[/usr/libexec/slapd],[$PATH:/sbin:/usr/sbin:/usr/libexec:/usr/lib/openldap:/opt/sfw/libexec:/opt/csw/libexec:/usr/local/libexec])])

DEFAULT_SLAPD_SCHEMA_DIR="/etc/openldap/schema"
# Check for openldap's schema files
AC_ARG_WITH([slapd-schema],
	AC_HELP_STRING([--with-slapd-schema=DIR],
		[Path to slapd schema directory]),
	[DEFAULT_SLAPD_SCHEMA_DIR=$withval],
	[
		for conf in /etc /opt/sfw/etc /opt/csw/etc /usr/local/etc; do
			for ldap in openldap ldap; do
				if test -e "${conf}/${ldap}/schema/core.schema"; then
					DEFAULT_SLAPD_SCHEMA_DIR="${conf}/${ldap}/schema"
					break 2
				fi
			done
		done
	])
AC_SUBST(DEFAULT_SLAPD_SCHEMA_DIR)

# Check for apache's apxs
AC_PATH_PROG(APXS,[apxs],no,[$PATH:/sbin:/usr/sbin:/usr/local/apache2/bin:/usr/apache2/bin:/usr/lib/apache2/bin:/usr/local/apache/bin:/usr/lib/apache/bin:/usr/pkg/sbin:/opt/csw/apache/bin])

APACHE_MOD_DIR="/usr/local/apache2/libexec"
# Look for apache's module dir
AC_ARG_WITH([apache-modules],
	AC_HELP_STRING([--with-apache-modules=DIR],
		[Path to apache module directory]),
	[APACHE_MOD_DIR=$withval],
	[
		if test "x$APXS" != "xno"; then
			APACHE_MOD_DIR="`${APXS} -q LIBEXECDIR`"
		else
			for apache in apache2 apache; do
				for lib in lib64 lib; do
					APACHE_MOD_DIR="/usr/${lib}/${apache}"
					if test -d ${APACHE_MOD_DIR}; then
						break 2
					fi
				done
			done
		fi
	])
AC_SUBST(APACHE_MOD_DIR)
AC_MSG_CHECKING([for Apache module directory])
AC_MSG_RESULT([$APACHE_MOD_DIR])

htl10n_jsfiles=
for lang in en `sed -e "/^#/d" -e "s/#.*//" "$srcdir/po/LINGUAS"`; do
	htl10n_jsfiles="$htl10n_jsfiles src/www/l10n/$lang.js"
done
AC_SUBST(htl10n_jsfiles)

PTHREAD_CFLAGS=
PTHREAD_LIBS=
# Check taken from glib
AC_MSG_NOTICE(for pthread flags)

PTHREAD_LIBS="-pthread -lpthread"

case "$host" in
  *-freebsd*)
    AC_MSG_CHECKING(FreeBSD Release Date)
    if test -x /sbin/sysctl; then
      os_version=`/sbin/sysctl -n kern.osreldate`
    else
      os_version=000000
    fi
    AC_MSG_RESULT($os_version)
    # 502102 is when libc_r switched to libpthread (aka libkse).
    if test $os_version -ge "502102"; then
      PTHREAD_CFLAGS="-pthread"
      PTHREAD_LIBS="-pthread -lpthread"
    else
      PTHREAD_CFLAGS="-pthread -D_THREAD_SAFE -D_REENTRANT"
      PTHREAD_LIBS="-pthread -lc_r"
    fi
  ;;
  *darwin*)
    #As of 10.4.2, POSIX Unnamed Semaphores are not implemented, use Carbon Semaphores instead
    PTHREAD_LIBS="-lpthread -Wl,-framework -Wl,Carbon"
    PTHREAD_CFLAGS="-I/System/Library/Frameworks/CoreServices.framework/Frameworks/CarbonCore.framework/Headers"
    CFLAGS="$CFLAGS -DMACOSX -I/opt/local/include"
  ;;
esac
AC_MSG_CHECKING(pthread cflags)
AC_MSG_RESULT($PTHREAD_CFLAGS)
AC_MSG_CHECKING(pthread libs)
AC_MSG_RESULT($PTHREAD_LIBS)

AC_SUBST(PTHREAD_CFLAGS)
AC_SUBST(PTHREAD_LIBS)

case xyes in
x$ac_cv_c_bigendian)
XPL_BIG_ENDIAN=1
XPL_LITTLE_ENDIAN=0
;;
*)
XPL_BIG_ENDIAN=0
XPL_LITTLE_ENDIAN=1
;;
esac

AC_DEFINE_UNQUOTED(XPL_BIG_ENDIAN, $XPL_BIG_ENDIAN, [Whether the system is big endian])
AC_DEFINE_UNQUOTED(XPL_LITTLE_ENDIAN, $XPL_LITTLE_ENDIAN, [Whether the system is little endian])

AC_ARG_WITH([user],
	AC_HELP_STRING([--with-user=USERNAME],
		[run bongo as unprivileged user USERNAME]))
BONGO_USER="$with_user"
AC_DEFINE_UNQUOTED(BONGO_USER,"$BONGO_USER", [The username which bongo runs as.])
AC_SUBST(BONGO_USER)
info_bongo_user=$BONGO_USER
if test "x$BONGO_USER" = "x"; then
	info_bongo_user="(unspecified)"
fi

AC_ARG_ENABLE([conntrace],
	AC_HELP_STRING([--enable-conntrace],
		[enable verbose connection tracing]),
	[if test "x$enableval" != "x"; then
		AC_DEFINE_UNQUOTED([CONN_TRACE], [1], [enable verbose connection tracing])
	fi])

AC_ARG_ENABLE([memmgrleakreporting],
	AC_HELP_STRING([--enable-memmgrleakreporting],
		[enable leak tracking and reporting in memmgr]),
	[if test "x$enableval" != "x"; then
		AC_DEFINE_UNQUOTED([MEMMGR_DEBUG], [1], [enable leak tracking and reporting in memmgr])
	fi])

AC_ARG_ENABLE([mdbdebug],
	AC_HELP_STRING([--enable-mdbdebug],
		[enable highly insecure protocol commands that can be used to scale test mdb]),
	[if test "x$enableval" != "x"; then
		AC_DEFINE_UNQUOTED([MDB_DEBUG], [1], [enable highly insecure protocol commands that can be used to scale test mdb])
	fi])

AC_ARG_ENABLE([system-malloc],
	AC_HELP_STRING ([--enable-system-malloc],
		[do not use memmgr pools]),
	[if test "x$enableval" != "x"; then
		AC_DEFINE_UNQUOTED([SYSTEM_MALLOC], [1], [do not use memmgr pools])
	fi])

info_debug_cflags="no"
AC_ARG_ENABLE([debug-cflags],
	AC_HELP_STRING([--enable-debug-cflags],
		[enable compile flags that make things work better in gdb]),
	[if test "x$enableval" != "x"; then
		# this may need to switch on the operating system to set the
		# appropriate flags on non-Linux systems
		CFLAGS="$CFLAGS -g -g3 -O0 -gdwarf-2" ; export CFLAGS
		CXXFLAGS="$CXXFLAGS -g -g3 -O0 -gdwarf-2" ; export CXXFLAGS
		info_debug_cflags="yes"
	fi])

AC_CHECK_SIZEOF(char*)
AC_CHECK_SIZEOF(void*)
AC_CHECK_SIZEOF(int)
AC_CHECK_SIZEOF(long)

AC_DEFINE_UNQUOTED(SQLITE_PTR_SZ, $ac_cv_sizeof_charp, [Size of a char *])

case $ac_cv_sizeof_voidp in
$ac_cv_sizeof_long) pi_cast='(long)' pui_cast='(unsigned long)' ;;
*) pi_cast=''  pui_cast='' ;;
esac
AC_MSG_RESULT($pi_cast $pui_cast)

AC_DEFINE_UNQUOTED(XPL_POINTER_TO_INT_CAST, $pi_cast, [How to cast a pointer to an int])
AC_DEFINE_UNQUOTED(XPL_POINTER_TO_UINT_CAST, $pui_cast, [How to cast an unsigned pointer to an int])

LIBCURL_CHECK_CONFIG()
if test "x$LIBCURL" = "x"; then
	AC_MSG_ERROR(Cannot find cURL. Please install the cURL development library.)
else
	AC_MSG_RESULT(Using system cURL)
	info_curl="system"
	AC_SUBST(LIBCURL_CFLAGS, $LIBCURL_CPPFLAGS)
	AC_SUBST(LIBCURL_LIBS, $LIBCURL)
fi

# import libraries

ACX_CLUCENE()
if test "x$CLUCENE_LIBS" = "x"; then
	AC_MSG_RESULT(Using imported CLucene)
	info_clucene="import"
	AC_CONFIG_SUBDIRS([import/clucene])
	AC_SUBST(CLUCENE_BONGO_API, "1")
	AC_SUBST(CLUCENE_LIBS, "\$(top_srcdir)/import/clucene/src/libclucene.la")
	AC_SUBST(CLUCENE_CXXFLAGS, "-I\$(top_srcdir)/import/clucene/src")
else
	AC_MSG_RESULT(Using system CLucene)
	info_clucene="system"
fi

AC_CHECK_SQLITE3()
if test "x$SQLITE_LIBS" = "x"; then
	AC_MSG_RESULT(Using imported SQLite)
	info_sqlite="import"
	AC_CONFIG_SUBDIRS([import/sqlite3])
	AC_SUBST(SQLITE_CFLAGS, "-I\$(top_srcdir)/import/sqlite3")
	AC_SUBST(SQLITE_LIBS, "\$(top_srcdir)/import/sqlite3/libbongosqlite3.la")
else
	AC_MSG_RESULT(Using system SQLite)
	info_sqlite="system"
fi

info_libical="import"
AC_CONFIG_SUBDIRS([import/libical])
AC_SUBST(IMPORT_SUBDIRS, $subdirs)

# check for doxygen
AC_PATH_PROG(DOXYGEN, doxygen, NO_DOXYGEN)
if test "$DOXYGEN" = NO_DOXYGEN; then
    AC_MSG_NOTICE([Couldn't find Doxygen - documentation cannot be built])
fi

ac_configure_args="$ac_configure_args --enable-threadsafe --enable-cross-thread-connections --disable-tcl"

AC_OUTPUT([
Makefile
bongo.pc
import/log4c/Makefile
import/log4c/sd/Makefile
import/log4c/log4c/Makefile
include/Makefile
init/bongo.init.fc4
init/bongo.init.sles9
init/bongo.init.suse10
init/http-bongo.solaris-smf
init/svc-bongo.solaris-smf
po/Makefile.in
src/agents/alarm/Makefile
src/agents/antispam/Makefile
src/agents/avirus/Makefile
src/agents/calcmd/Makefile
src/agents/collector/Makefile
src/agents/connmgr/Makefile
src/agents/connmgr/cmmodules/lists/Makefile
src/agents/connmgr/cmmodules/rbl/Makefile
src/agents/connmgr/cmmodules/rdns/Makefile
src/agents/connmgr/cmmodules/user/Makefile
src/agents/generic/Makefile
src/agents/imap/Makefile
src/agents/imap/tests/Makefile
src/agents/itip/Makefile
src/agents/mailprox/Makefile
src/agents/pluspack/Makefile
src/agents/pop/Makefile
src/agents/queue/Makefile
src/agents/smtp/Makefile
src/agents/store/Makefile
src/agents/store/tests/Makefile
src/agents/rules/Makefile
src/apps/setup/Makefile
src/apps/setup/bongo-setup.xml
src/apps/manager/Makefile
src/apps/admin/Makefile
src/apps/config/Makefile
src/apps/sendmail/Makefile
src/apps/backup/Makefile
src/apps/mdbtool/Makefile
src/apps/queuetool/Makefile
src/apps/storetool/Makefile
src/apps/storetool/demo/Makefile
src/libs/cal/Makefile
src/libs/cal/tests/Makefile
src/libs/calcmd/Makefile
src/libs/calcmd/tests/Makefile
src/libs/connio/Makefile
src/libs/connio/tests/Makefile
src/libs/json/Makefile
src/libs/streamio/Makefile
src/libs/streamio/codec/Makefile
src/libs/logger/Makefile
src/libs/management/Makefile
src/libs/mdb/Makefile
src/libs/mdb-file/Makefile
src/libs/mdb-xldap/Makefile
src/libs/memmgr/Makefile
src/libs/msgapi/Makefile
src/libs/nmap/Makefile
src/libs/python/Makefile
src/libs/python/bongo-python-wrapper
src/libs/python/bongo/Makefile
src/libs/python/bongo/Xpl.py
src/libs/bongoutil/Makefile
src/libs/bongoutil/tests/Makefile
src/libs/xpl/Makefile
src/libs/connmgr/Makefile
src/www/Makefile
src/www/bongo.conf
zoneinfo/Makefile
man/Makefile
])

info_bongo_version=`$AWK '{ if ($ 1 == "#define" && $ 2 == "BONGO_BUILD_VER") print $ 3; }' < "./include/bongo-buildinfo.h" | tr -d '"'`

AC_MSG_NOTICE([Configured to build Bongo:

  Bongo version:   ${info_bongo_version}
  Install prefix:  ${prefix}
  Compiler:        ${CC}
  Bongo user:      ${info_bongo_user}
  Debug CFLAGS:    ${info_debug_cflags}

Imported libraries:
  CLucene:         ${info_clucene}
  CURL:            ${info_curl}
  SQLite:          ${info_sqlite}
  libical:         ${info_libical}
])
