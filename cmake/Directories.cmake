# various directory definitions we use throughout Bongo

if(EXISTS /lib64)
	set(_LIB_DIR_NAME lib64)
else(EXISTS /lib64)
	set(_LIB_DIR_NAME lib)
endif(EXISTS /lib64)

set(LIB_DIR_NAME ${_LIB_DIR_NAME} CACHE PATH 
	"Name of library location (arch-specific), usually 'lib' or 'lib64'")

# 'default' locations for where objects get built
set(BINDIR			${PROJECT_BINARY_DIR}/bin)
set(SBINDIR			${PROJECT_BINARY_DIR}/sbin)
set(LIBDIR			${PROJECT_BINARY_DIR}/${LIB_DIR_NAME})
set(LIBEXECDIR			${PROJECT_BINARY_DIR}/libexec)
set(DATAROOTDIR			${PROJECT_BINARY_DIR}/share)
set(DATADIR			${PROJECT_BINARY_DIR}/share)
set(LOCALSTATEDIR		${PROJECT_BINARY_DIR}/var)
set(INCLUDEDIR			${PROJECT_BINARY_DIR}/include)

# various paths for where objects get installed / work from
set(XPL_DEFAULT_DATA_DIR	${CMAKE_INSTALL_PREFIX}/share/bongo)
set(XPL_DEFAULT_STATE_DIR	${CMAKE_INSTALL_PREFIX}/var/bongo)

set(XPL_DEFAULT_BIN_DIR		${CMAKE_INSTALL_PREFIX}/sbin)
set(XPL_DEFAULT_CONF_DIR	${CMAKE_INSTALL_PREFIX}/etc/bongo)
set(XPL_DEFAULT_NLS_DIR		${CMAKE_INSTALL_PREFIX}/share/bongo/nls)

set(XPL_DEFAULT_DBF_DIR		${XPL_DEFAULT_STATE_DIR}/dbf)
set(XPL_DEFAULT_WORK_DIR	${XPL_DEFAULT_STATE_DIR}/work)
set(XPL_DEFAULT_CACHE_DIR	${XPL_DEFAULT_STATE_DIR}/cache)
set(XPL_DEFAULT_SCMS_DIR	${XPL_DEFAULT_STATE_DIR}/scms)
set(XPL_DEFAULT_SPOOL_DIR	${XPL_DEFAULT_STATE_DIR}/spool)
set(XPL_DEFAULT_STORE_SYSTEM_DIR	${XPL_DEFAULT_STATE_DIR}/system)
set(XPL_DEFAULT_SYSTEM_DIR	${XPL_DEFAULT_STATE_DIR}/system)
set(XPL_DEFAULT_MAIL_DIR	${XPL_DEFAULT_STATE_DIR}/users)
set(XPL_DEFAULT_COOKIE_DIR	${XPL_DEFAULT_STATE_DIR}/dbf/cookies)

set(XPL_DEFAULT_CERT_PATH	${XPL_DEFAULT_STATE_DIR}/dbf/osslcert.pem)
set(XPL_DEFAULT_RSAPARAMS_PATH	${XPL_DEFAULT_STATE_DIR}/dbf/rsaparams.pem)
set(XPL_DEFAULT_DHPARAMS_PATH	${XPL_DEFAULT_STATE_DIR}/dbf/dhparams.pem)
set(XPL_DEFAULT_RANDSEED_PATH	${XPL_DEFAULT_STATE_DIR}/dbf/randomseed)
set(XPL_DEFAULT_KEY_PATH	${XPL_DEFAULT_STATE_DIR}/dbf/osslpriv.pem)

# FIXME: where did these get defined before?
set(LOCALEDIR			${CMAKE_INSTALL_PREFIX}/share/locale)
set(ZONEINFODIR			${XPL_DEFAULT_DATA_DIR}/zoneinfo)
