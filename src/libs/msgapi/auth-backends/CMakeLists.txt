add_library(authsqlite3 MODULE
	sqlite.c)

install(TARGETS authsqlite3 DESTINATION ${LIB_INSTALL_DIR}/bongo-auth)
