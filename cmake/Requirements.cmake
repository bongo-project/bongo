# include here the requirements to build Bongo
# anything which isn't necessary should be marked [optional]

include(CheckIncludeFile) 
include(CheckLibraryExists)
include(FindPkgConfig)

# look for header files we need first
check_include_file(inttypes.h HAVE_INTTYPES_H)
check_include_file(sys/kstat.h HAVE_KSTAT_H)
check_include_file(sys/vfs.h HAVE_SYS_VFS_H)
check_include_file(sys/stat.h HAVE_SYS_STAT_H)
check_include_file(sys/statvfs.h HAVE_SYS_STATVFS_H)
check_include_file(time.h HAVE_TIME_H)
check_include_file(semaphore.h HAVE_SEMAPHORE_H)

# look for zlib
find_library(HAVE_ZLIB NAMES z zlib)
if (NOT HAVE_ZLIB)
	message(FATAL_ERROR "Bongo requires zlib")
endif (NOT HAVE_ZLIB)

# look for PThreads
include(FindThreads)
if (NOT CMAKE_USE_PTHREADS_INIT)
	message(FATAL_ERROR "Bongo requires a platform with PThreads")
endif (NOT CMAKE_USE_PTHREADS_INIT)

# check for LDAP [optional]
check_include_file(ldap.h HAVE_LDAP_H)
if (HAVE_LDAP_H)
	check_library_exists(ldap ldap_init "" HAVE_LDAP)
endif (HAVE_LDAP_H)

# check for ODBC [optional]
check_include_file(odbcinst.h HAVE_ODBCINST_H)
if (HAVE_ODBCINST_H)
	check_library_exists(odbc SQLConnect "" HAVE_ODBC)
endif (HAVE_ODBCINST_H)

# check for resolv
#check_library_exists(resolv res_init "" HAVE_RESOLV)
#if (NOT HAVE_RESOLV)
#	message(FATAL_ERROR "Cannot find resolv library")
#endif (NOT HAVE_RESOLV)

# check for gettext
check_include_file(libintl.h HAVE_LIBINTL_H)
include (FindGettext)
if (NOT GETTEXT_FOUND)
	message(FATAL_ERROR "Bongo needs Gettext developer support available")
endif (NOT GETTEXT_FOUND)

check_include_file(kstat.h HAVE_KSTAT_H)

# check for log4c
include(cmake/FindLog4C.cmake)
include_directories(AFTER ${CMAKE_CURRENT_SOURCE_DIR}/import/log4c/)

# check for glib
pkg_check_modules (GLIB2 REQUIRED glib-2.0>=2.10)
include_directories(AFTER ${GLIB2_INCLUDE_DIRS})

# check for GMime
pkg_search_module (GMIME2 REQUIRED gmime-2.6 gmime-2.4 gmime-2.2 gmime-2.0 gmime)
include_directories(AFTER ${GMIME2_INCLUDE_DIRS})

# check for gcrypt
check_library_exists(gcrypt gcry_control "" HAVE_GCRYPT)
if (HAVE_GCRYPT)
	set(GCRYPT_LIBRARIES "gcrypt")
else (HAVE_GCRYPT)
	message(FATAL_ERROR "Bongo couldn't find the libgcrypt runtime library")
endif (HAVE_GCRYPT)

# check for gnutls
pkg_check_modules (GNUTLS REQUIRED gnutls)

# check for sqlite3
pkg_check_modules (SQLITE REQUIRED sqlite3)

# check for curl
pkg_check_modules (CURL REQUIRED libcurl)

# check for libical
check_include_file(libical/ical.h HAVE_ICAL_H)
if (NOT HAVE_ICAL_H)
	check_include_file(ical.h HAVE_OLD_ICAL_H)
	if (NOT HAVE_OLD_ICAL_H)
		message(FATAL_ERROR "Bongo needs libical in order to compile")
	endif (NOT HAVE_OLD_ICAL_H)
endif (NOT HAVE_ICAL_H)

check_library_exists(ical icaltime_compare "" HAVE_ICAL)
if (HAVE_ICAL)
	set(LIBICAL_LIBRARIES "ical")
else (HAVE_ICAL)
	message(FATAL_ERROR "Bongo couldn't find the libical runtime library")
endif (HAVE_ICAL)

# check for Python
include(FindPythonLibs)
if (NOT PYTHONLIBS_FOUND)
	message(FATAL_ERROR "Bongo requires Python development libraries installed")
endif (NOT PYTHONLIBS_FOUND)

execute_process(COMMAND python -c "from distutils.sysconfig import get_python_lib; print get_python_lib()"
	RESULT_VARIABLE PYTHON_SITEPACKAGES_FOUND
	OUTPUT_VARIABLE PYTHON_SITEPACKAGES_PATH_RAW
)
string(STRIP "${PYTHON_SITEPACKAGES_PATH_RAW}" PYTHON_SITEPACKAGES_PATH_RAW)

execute_process(COMMAND python -c "from distutils.sysconfig import get_python_lib; print get_python_lib(1)"
	RESULT_VARIABLE PYTHON_SITELIB_FOUND
	OUTPUT_VARIABLE PYTHON_SITELIB_PATH_RAW
)
string(STRIP "${PYTHON_SITELIB_PATH_RAW}" PYTHON_SITELIB_PATH_RAW)

string(LENGTH "${CMAKE_INSTALL_PREFIX}" CMAKE_INSTALL_PREFIX_LEN)
string(SUBSTRING "${PYTHON_SITEPACKAGES_PATH_RAW}" 0 ${CMAKE_INSTALL_PREFIX_LEN} PYTHON_SITEPACKAGES_PATH_RAW_PREFIX)

if (PYTHON_SITEPACKAGES_FOUND EQUAL 0)
	if (PYTHON_SITEPACKAGES_PATH_RAW_PREFIX STREQUAL CMAKE_INSTALL_PREFIX)
		set(PYTHON_SITEPACKAGES_PATH "${PYTHON_SITEPACKAGES_PATH_RAW}")
		set(PYTHON_SITELIB_PATH "${PYTHON_SITELIB_PATH_RAW}")
	else ()
		set(PYTHON_SITEPACKAGES_PATH "${CMAKE_INSTALL_PREFIX}/${PYTHON_SITEPACKAGES_PATH_RAW}")
		set(PYTHON_SITELIB_PATH "${CMAKE_INSTALL_PREFIX}/${PYTHON_SITELIB_PATH_RAW}")
	endif ()
else (PYTHON_SITEPACKAGES_FOUND EQUAL 0)
	message(FATAL_ERROR "Couldn't determine where to install Python modules")
endif (PYTHON_SITEPACKAGES_FOUND EQUAL 0)

# check for Doxygen [optional]
include(FindDoxygen)
