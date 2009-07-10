add_library(bongologging SHARED
	logging.c
	)

install(TARGETS bongologging DESTINATION ${LIB_INSTALL_DIR})
