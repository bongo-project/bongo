add_library(bongojson SHARED
	bongojsonrpc.c
	json.c
	parse.c)

install(TARGETS bongojson DESTINATION ${LIB_INSTALL_DIR})
