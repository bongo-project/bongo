#ifndef _INCLUDE_BONGO_CONFIG_H
#define _INCLUDE_BONGO_CONFIG_H 1
 
/*
include/bongo-config.h.
Generated
automatically
at
end
of
configure.
*/
/* config.h.  Generated from config.h.in by configure.  */
/* config.h.in.  Generated from configure.ac by autoheader.  */

/* Controls how tests that depend on check compile. */
/* #undef _BONGO_BONGO_HAVE_CHECK */

/* The username which bongo runs as. */
#ifndef _BONGO_BONGO_USER 
#define _BONGO_BONGO_USER  "" 
#endif

/* enable verbose connection tracing */
/* #undef _BONGO_CONN_TRACE */

/* Define to 1 if translation of program messages to the user's native
   language is requested. */
#ifndef _BONGO_ENABLE_NLS 
#define _BONGO_ENABLE_NLS  1 
#endif

/* The name of the gettext domain */
#ifndef _BONGO_GETTEXT_PACKAGE 
#define _BONGO_GETTEXT_PACKAGE  "bongo" 
#endif

/* Define to 1 if you have the <alloca.h> header file. */
#ifndef _BONGO_HAVE_ALLOCA_H 
#define _BONGO_HAVE_ALLOCA_H  1 
#endif

/* Define to 1 if you have the MacOS X function CFLocaleCopyCurrent in the
   CoreFoundation framework. */
/* #undef _BONGO_HAVE_CFLOCALECOPYCURRENT */

/* Define to 1 if you have the MacOS X function CFPreferencesCopyAppValue in
   the CoreFoundation framework. */
/* #undef _BONGO_HAVE_CFPREFERENCESCOPYAPPVALUE */

/* Define to 1 if you have the `consoleprintf' function. */
/* #undef _BONGO_HAVE_CONSOLEPRINTF */

/* Define if the GNU dcgettext() function is already present or preinstalled.
   */
#ifndef _BONGO_HAVE_DCGETTEXT 
#define _BONGO_HAVE_DCGETTEXT  1 
#endif

/* Define to 1 if you have the declaration of `ns_c_in', and to 0 if you
   don't. */
#ifndef _BONGO_HAVE_DECL_NS_C_IN 
#define _BONGO_HAVE_DECL_NS_C_IN  1 
#endif

/* Define to 1 if you have the `delay' function. */
/* #undef _BONGO_HAVE_DELAY */

/* Define to 1 if you have the <dlfcn.h> header file. */
#ifndef _BONGO_HAVE_DLFCN_H 
#define _BONGO_HAVE_DLFCN_H  1 
#endif

/* Define to 1 if you have the `dlopen' function. */
/* #undef _BONGO_HAVE_DLOPEN */

/* Define to 1 if you have the `dlsym' function. */
/* #undef _BONGO_HAVE_DLSYM */

/* Define to 1 if you have the `dos2calendar' function. */
/* #undef _BONGO_HAVE_DOS2CALENDAR */

/* Define to 1 if you have the `FEFlushWrite' function. */
/* #undef _BONGO_HAVE_FEFLUSHWRITE */

/* Define to 1 if you have the `fsync' function. */
#ifndef _BONGO_HAVE_FSYNC 
#define _BONGO_HAVE_FSYNC  1 
#endif

/* Define to 1 if you have the `getpwnam_r' function. */
#ifndef _BONGO_HAVE_GETPWNAM_R 
#define _BONGO_HAVE_GETPWNAM_R  1 
#endif

/* Define if the GNU gettext() function is already present or preinstalled. */
#ifndef _BONGO_HAVE_GETTEXT 
#define _BONGO_HAVE_GETTEXT  1 
#endif

/* Define to 1 if you have the `gettimeofday' function. */
#ifndef _BONGO_HAVE_GETTIMEOFDAY 
#define _BONGO_HAVE_GETTIMEOFDAY  1 
#endif

/* Define to 1 if you have the `gmtime_r' function. */
#ifndef _BONGO_HAVE_GMTIME_R 
#define _BONGO_HAVE_GMTIME_R  1 
#endif

/* Define if you have the iconv() function. */
/* #undef _BONGO_HAVE_ICONV */

/* Define to 1 if you have the <inttypes.h> header file. */
#ifndef _BONGO_HAVE_INTTYPES_H 
#define _BONGO_HAVE_INTTYPES_H  1 
#endif

/* Define if you have liblogevent */
/* #undef _BONGO_HAVE_LIBLOGEVENT */

/* Define to 1 if you have the `resolv' library (-lresolv). */
#ifndef _BONGO_HAVE_LIBRESOLV 
#define _BONGO_HAVE_LIBRESOLV  1 
#endif

/* Define to 1 if you have the `localtime_r' function. */
#ifndef _BONGO_HAVE_LOCALTIME_R 
#define _BONGO_HAVE_LOCALTIME_R  1 
#endif

/* Define to 1 if you have the <memory.h> header file. */
#ifndef _BONGO_HAVE_MEMORY_H 
#define _BONGO_HAVE_MEMORY_H  1 
#endif

/* Define to 1 if you have the `nanosleep' function. */
#ifndef _BONGO_HAVE_NANOSLEEP 
#define _BONGO_HAVE_NANOSLEEP  1 
#endif

/* Define to 1 if you have the <netinet/in.h> header file. */
#ifndef _BONGO_HAVE_NETINET_IN_H 
#define _BONGO_HAVE_NETINET_IN_H  1 
#endif

/* Define to 1 if you have the <Python.h> header file. */
#ifndef _BONGO_HAVE_PYTHON_H 
#define _BONGO_HAVE_PYTHON_H  1 
#endif

/* Define to 1 if you have the <resolv.h> header file. */
#ifndef _BONGO_HAVE_RESOLV_H 
#define _BONGO_HAVE_RESOLV_H  1 
#endif

/* Define to 1 if you have the `RingTheBell' function. */
/* #undef _BONGO_HAVE_RINGTHEBELL */

/* Define to 1 if you have the <semaphore.h> header file. */
#ifndef _BONGO_HAVE_SEMAPHORE_H 
#define _BONGO_HAVE_SEMAPHORE_H  1 
#endif

/* Define to 1 if you have the `snprintf' function. */
#ifndef _BONGO_HAVE_SNPRINTF 
#define _BONGO_HAVE_SNPRINTF  1 
#endif

/* Define to 1 if you have the `statfs' function. */
#ifndef _BONGO_HAVE_STATFS 
#define _BONGO_HAVE_STATFS  1 
#endif

/* Define to 1 if you have the `statvfs' function. */
#ifndef _BONGO_HAVE_STATVFS 
#define _BONGO_HAVE_STATVFS  1 
#endif

/* Define to 1 if you have the <stdint.h> header file. */
#ifndef _BONGO_HAVE_STDINT_H 
#define _BONGO_HAVE_STDINT_H  1 
#endif

/* Define to 1 if you have the <stdlib.h> header file. */
#ifndef _BONGO_HAVE_STDLIB_H 
#define _BONGO_HAVE_STDLIB_H  1 
#endif

/* Define to 1 if you have the `strcasecmp' function. */
#ifndef _BONGO_HAVE_STRCASECMP 
#define _BONGO_HAVE_STRCASECMP  1 
#endif

/* Define to 1 if you have the `stricmp' function. */
/* #undef _BONGO_HAVE_STRICMP */

/* Define to 1 if you have the <strings.h> header file. */
#ifndef _BONGO_HAVE_STRINGS_H 
#define _BONGO_HAVE_STRINGS_H  1 
#endif

/* Define to 1 if you have the <string.h> header file. */
#ifndef _BONGO_HAVE_STRING_H 
#define _BONGO_HAVE_STRING_H  1 
#endif

/* Define to 1 if you have the `strncasecmp' function. */
#ifndef _BONGO_HAVE_STRNCASECMP 
#define _BONGO_HAVE_STRNCASECMP  1 
#endif

/* Define to 1 if you have the `strnicmp' function. */
/* #undef _BONGO_HAVE_STRNICMP */

/* Define to 1 if you have the <syslog.h> header file. */
#ifndef _BONGO_HAVE_SYSLOG_H 
#define _BONGO_HAVE_SYSLOG_H  1 
#endif

/* Define to 1 if you have the <sys/mount.h> header file. */
#ifndef _BONGO_HAVE_SYS_MOUNT_H 
#define _BONGO_HAVE_SYS_MOUNT_H  1 
#endif

/* Define to 1 if you have the <sys/param.h> header file. */
#ifndef _BONGO_HAVE_SYS_PARAM_H 
#define _BONGO_HAVE_SYS_PARAM_H  1 
#endif

/* Define to 1 if you have the <sys/socket.h> header file. */
#ifndef _BONGO_HAVE_SYS_SOCKET_H 
#define _BONGO_HAVE_SYS_SOCKET_H  1 
#endif

/* Define to 1 if you have the <sys/statvfs.h> header file. */
#ifndef _BONGO_HAVE_SYS_STATVFS_H 
#define _BONGO_HAVE_SYS_STATVFS_H  1 
#endif

/* Define to 1 if you have the <sys/stat.h> header file. */
#ifndef _BONGO_HAVE_SYS_STAT_H 
#define _BONGO_HAVE_SYS_STAT_H  1 
#endif

/* Define to 1 if you have the <sys/sysinfo.h> header file. */
#ifndef _BONGO_HAVE_SYS_SYSINFO_H 
#define _BONGO_HAVE_SYS_SYSINFO_H  1 
#endif

/* Define to 1 if you have the <sys/sys/mount.h> header file. */
/* #undef _BONGO_HAVE_SYS_SYS_MOUNT_H */

/* Define to 1 if you have the <sys/types.h> header file. */
#ifndef _BONGO_HAVE_SYS_TYPES_H 
#define _BONGO_HAVE_SYS_TYPES_H  1 
#endif

/* Define to 1 if you have the <sys/vfs.h> header file. */
#ifndef _BONGO_HAVE_SYS_VFS_H 
#define _BONGO_HAVE_SYS_VFS_H  1 
#endif

/* Define to 1 if you have the `ungetcharacter' function. */
/* #undef _BONGO_HAVE_UNGETCHARACTER */

/* Define to 1 if you have the <unistd.h> header file. */
#ifndef _BONGO_HAVE_UNISTD_H 
#define _BONGO_HAVE_UNISTD_H  1 
#endif

/* Define to 1 if you have the `usleep' function. */
#ifndef _BONGO_HAVE_USLEEP 
#define _BONGO_HAVE_USLEEP  1 
#endif

/* Define to 1 if you have the `vsnprintf' function. */
#ifndef _BONGO_HAVE_VSNPRINTF 
#define _BONGO_HAVE_VSNPRINTF  1 
#endif

/* Define to 1 if you have the `_commit' function. */
/* #undef _BONGO_HAVE__COMMIT */

/* Define to 1 if you have the `_ConvertDOSTimeToCalendar' function. */
/* #undef _BONGO_HAVE__CONVERTDOSTIMETOCALENDAR */

/* Define to 1 if you have the `_snprintf' function. */
/* #undef _BONGO_HAVE__SNPRINTF */

/* Define to 1 if you have the `_vsnprintf' function. */
/* #undef _BONGO_HAVE__VSNPRINTF */

/* Badly named building-under-autoconf variable */
#ifndef _BONGO_LINUX 
#define _BONGO_LINUX  "1" 
#endif

/* enable highly insecure protocol commands that can be used to scale test mdb
   */
/* #undef _BONGO_MDB_DEBUG */

/* enable leak tracking and reporting in memmgr */
/* #undef _BONGO_MEMMGR_DEBUG */

/* Define to 1 if your C compiler doesn't accept -c and -o together. */
/* #undef _BONGO_NO_MINUS_C_MINUS_O */

/* Name of package */
#ifndef _BONGO_PACKAGE 
#define _BONGO_PACKAGE  "bongo" 
#endif

/* Define to the address where bug reports for this package should be sent. */
#ifndef _BONGO_PACKAGE_BUGREPORT 
#define _BONGO_PACKAGE_BUGREPORT  "" 
#endif

/* Define to the full name of this package. */
#ifndef _BONGO_PACKAGE_NAME 
#define _BONGO_PACKAGE_NAME  "" 
#endif

/* Define to the full name and version of this package. */
#ifndef _BONGO_PACKAGE_STRING 
#define _BONGO_PACKAGE_STRING  "" 
#endif

/* Define to the one symbol short name of this package. */
#ifndef _BONGO_PACKAGE_TARNAME 
#define _BONGO_PACKAGE_TARNAME  "" 
#endif

/* Define to the version of this package. */
#ifndef _BONGO_PACKAGE_VERSION 
#define _BONGO_PACKAGE_VERSION  "" 
#endif

/* The size of `char*', as computed by sizeof. */
#ifndef _BONGO_SIZEOF_CHARP 
#define _BONGO_SIZEOF_CHARP  4 
#endif

/* The size of `int', as computed by sizeof. */
#ifndef _BONGO_SIZEOF_INT 
#define _BONGO_SIZEOF_INT  4 
#endif

/* The size of `long', as computed by sizeof. */
#ifndef _BONGO_SIZEOF_LONG 
#define _BONGO_SIZEOF_LONG  4 
#endif

/* The size of `void*', as computed by sizeof. */
#ifndef _BONGO_SIZEOF_VOIDP 
#define _BONGO_SIZEOF_VOIDP  4 
#endif

/* Size of a char * */
#ifndef _BONGO_SQLITE_PTR_SZ 
#define _BONGO_SQLITE_PTR_SZ  4 
#endif

/* Define to 1 if you have the ANSI C header files. */
#ifndef _BONGO_STDC_HEADERS 
#define _BONGO_STDC_HEADERS  1 
#endif

/* do not use memmgr pools */
/* #undef _BONGO_SYSTEM_MALLOC */

/* Version number of package */
#ifndef _BONGO_VERSION 
#define _BONGO_VERSION  "0.1.0" 
#endif

/* Define to 1 if your processor stores words with the most significant byte
   first (like Motorola and SPARC, unlike Intel and VAX). */
/* #undef _BONGO_WORDS_BIGENDIAN */

/* Whether the system is big endian */
#ifndef _BONGO_XPL_BIG_ENDIAN 
#define _BONGO_XPL_BIG_ENDIAN  0 
#endif

/* Default binary directory */
#ifndef _BONGO_XPL_DEFAULT_BIN_DIR 
#define _BONGO_XPL_DEFAULT_BIN_DIR  "/usr/local/sbin" 
#endif

/* Default cache directory */
#ifndef _BONGO_XPL_DEFAULT_CACHE_DIR 
#define _BONGO_XPL_DEFAULT_CACHE_DIR  "/usr/local/var/bongo/cache" 
#endif

/* Default cert path */
#ifndef _BONGO_XPL_DEFAULT_CERT_PATH 
#define _BONGO_XPL_DEFAULT_CERT_PATH  "/usr/local/var/bongo/dbf/osslcert.pem" 
#endif

/* Default config directory */
#ifndef _BONGO_XPL_DEFAULT_CONF_DIR 
#define _BONGO_XPL_DEFAULT_CONF_DIR  "/usr/local/etc/bongo" 
#endif

/* Default data directory */
#ifndef _BONGO_XPL_DEFAULT_DATA_DIR 
#define _BONGO_XPL_DEFAULT_DATA_DIR  "/usr/local/share/bongo" 
#endif

/* Default dbf directory */
#ifndef _BONGO_XPL_DEFAULT_DBF_DIR 
#define _BONGO_XPL_DEFAULT_DBF_DIR  "/usr/local/var/bongo/dbf" 
#endif

/* Default key path */
#ifndef _BONGO_XPL_DEFAULT_KEY_PATH 
#define _BONGO_XPL_DEFAULT_KEY_PATH  "/usr/local/var/bongo/dbf/osslpriv.pem" 
#endif

/* Default library directory */
#ifndef _BONGO_XPL_DEFAULT_LIB_DIR 
#define _BONGO_XPL_DEFAULT_LIB_DIR  "/usr/local/lib" 
#endif

/* Default mail directory */
#ifndef _BONGO_XPL_DEFAULT_MAIL_DIR 
#define _BONGO_XPL_DEFAULT_MAIL_DIR  "/usr/local/var/bongo/users" 
#endif

/* Default nls directory */
#ifndef _BONGO_XPL_DEFAULT_NLS_DIR 
#define _BONGO_XPL_DEFAULT_NLS_DIR  "/usr/local/share/bongo/nls" 
#endif

/* Default scms directory */
#ifndef _BONGO_XPL_DEFAULT_SCMS_DIR 
#define _BONGO_XPL_DEFAULT_SCMS_DIR  "/usr/local/var/bongo/scms" 
#endif

/* Default spool directory */
#ifndef _BONGO_XPL_DEFAULT_SPOOL_DIR 
#define _BONGO_XPL_DEFAULT_SPOOL_DIR  "/usr/local/var/bongo/spool" 
#endif

/* Default state directory */
#ifndef _BONGO_XPL_DEFAULT_STATE_DIR 
#define _BONGO_XPL_DEFAULT_STATE_DIR  "/usr/local/var/bongo" 
#endif

/* Default store system directory */
#ifndef _BONGO_XPL_DEFAULT_STORE_SYSTEM_DIR 
#define _BONGO_XPL_DEFAULT_STORE_SYSTEM_DIR  "/usr/local/var/bongo/system" 
#endif

/* Default work directory */
#ifndef _BONGO_XPL_DEFAULT_WORK_DIR 
#define _BONGO_XPL_DEFAULT_WORK_DIR  "/usr/local/var/bongo/work" 
#endif

/* Whether the system is little endian */
#ifndef _BONGO_XPL_LITTLE_ENDIAN 
#define _BONGO_XPL_LITTLE_ENDIAN  1 
#endif

/* How to cast a pointer to an int */
#ifndef _BONGO_XPL_POINTER_TO_INT_CAST 
#define _BONGO_XPL_POINTER_TO_INT_CAST  (long) 
#endif

/* How to cast an unsigned pointer to an int */
#ifndef _BONGO_XPL_POINTER_TO_UINT_CAST 
#define _BONGO_XPL_POINTER_TO_UINT_CAST  (unsigned long) 
#endif

/* Define to 1 if `lex' declares `yytext' as a `char *' by default, not a
   `char[]'. */
#ifndef _BONGO_YYTEXT_POINTER 
#define _BONGO_YYTEXT_POINTER  1 
#endif
 
/* once:
_INCLUDE_BONGO_CONFIG_H
*/
#endif
