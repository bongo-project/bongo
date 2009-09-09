/* The username which bongo runs as. */
#cmakedefine BONGO_USER	"@BONGO_USER@"

// our package name
#cmakedefine PACKAGE	"@CMAKE_PROJECT_NAME@"

// the way we do includes on the python code seems to cause problems
//  with the cmake style of defines.
// include defines
#ifndef HAVE_INTTYPES_H
    #cmakedefine HAVE_INTTYPES_H
#endif

#ifndef HAVE_SYS_STATVFS_H
    #cmakedefine HAVE_SYS_STATVFS_H
#endif

#ifndef HAVE_SYS_STAT_H
    #cmakedefine HAVE_SYS_STAT_H
#endif

#ifndef HAVE_SYS_VFS_H
    #cmakedefine HAVE_SYS_VFS_H
#endif

#ifndef HAVE_KSTAT_H
    #cmakedefine HAVE_KSTAT_H
#endif

#ifndef HAVE_SEMAPHORE_H
    #cmakedefine HAVE_SEMAPHORE_H
#endif// Directories.txt

#cmakedefine HAVE_ICAL_H	1
#cmakedefine HAVE_OLD_ICAL_H	1

#cmakedefine BINDIR		"@BINDIR@"
#cmakedefine SBINDIR		"@SBINDIR@"
#cmakedefine LIBDIR		"@LIBDIR@"
#cmakedefine LIBEXECDIR		"@LIBEXECDIR@"
#cmakedefine DATAROOTDIR	"@DATAROOTDIR@"
#cmakedefine DATADIR		"@DATADIR@"
#cmakedefine LOCALSTATEDIR	"@LOCALSTATEDIR@"
#cmakedefine INCLUDEDIR		"@INCLUDEDIR@"

#cmakedefine XPL_DEFAULT_DATA_DIR	"@XPL_DEFAULT_DATA_DIR@"
#cmakedefine XPL_DEFAULT_STATE_DIR	"@XPL_DEFAULT_STATE_DIR@"

#cmakedefine XPL_DEFAULT_BIN_DIR	"@XPL_DEFAULT_BIN_DIR@"
#cmakedefine XPL_DEFAULT_CONF_DIR	"@XPL_DEFAULT_CONF_DIR@"
#cmakedefine XPL_DEFAULT_NLS_DIR	"@XPL_DEFAULT_NLS_DIR@"

#cmakedefine XPL_DEFAULT_DBF_DIR	"@XPL_DEFAULT_DBF_DIR@"
#cmakedefine XPL_DEFAULT_WORK_DIR	"@XPL_DEFAULT_WORK_DIR@"
#cmakedefine XPL_DEFAULT_CACHE_DIR	"@XPL_DEFAULT_CACHE_DIR@"
#cmakedefine XPL_DEFAULT_SCMS_DIR	"@XPL_DEFAULT_SCMS_DIR@"
#cmakedefine XPL_DEFAULT_STORE_SYSTEM_DIR	"@XPL_DEFAULT_STORE_SYSTEM_DIR@"
#cmakedefine XPL_DEFAULT_SPOOL_DIR	"@XPL_DEFAULT_SPOOL_DIR@"
#cmakedefine XPL_DEFAULT_SYSTEM_DIR	"@XPL_DEFAULT_SYSTEM_DIR@"
#cmakedefine XPL_DEFAULT_MAIL_DIR	"@XPL_DEFAULT_MAIL_DIR@"
#cmakedefine XPL_DEFAULT_COOKIE_DIR	"@XPL_DEFAULT_COOKIE_DIR@"

#cmakedefine XPL_DEFAULT_CERT_PATH	"@XPL_DEFAULT_CERT_PATH@"
#cmakedefine XPL_DEFAULT_RSAPARAMS_PATH	"@XPL_DEFAULT_RSAPARAMS_PATH@"
#cmakedefine XPL_DEFAULT_DHPARAMS_PATH	"@XPL_DEFAULT_DHPARAMS_PATH@"
#cmakedefine XPL_DEFAULT_RANDSEED_PATH	"@XPL_DEFAULT_RANDSEED_PATH@"
#cmakedefine XPL_DEFAULT_KEY_PATH	"@XPL_DEFAULT_KEY_PATH@"

#cmakedefine LOCALEDIR	"@LOCALEDIR@"
#cmakedefine ZONEINFODIR	"@ZONEINFODIR@"

#cmakedefine BONGO_BUILD_BRANCH		"@BONGO_BUILD_BRANCH@"
#cmakedefine BONGO_BUILD_VSTR		"@BONGO_BUILD_VSTR@"
#cmakedefine BONGO_BUILD_VER		"@BONGO_BUILD_VER@"

// Legacy defines below
#cmakedefine	LINUX
#cmakedefine	UNIX
#cmakedefine	HAVE_NANOSLEEP
#cmakedefine	_BONGO_XPL_BIG_ENDIAN
#cmakedefine	_BONGO_XPL_LITTLE_ENDIAN
#cmakedefine	_BONGO_HAVE_DLFCN_H
