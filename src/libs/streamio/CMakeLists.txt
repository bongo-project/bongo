add_library(bongostreamio SHARED
	streamio.c
	search.c
	bongostream.c)

install(TARGETS bongostreamio DESTINATION ${LIB_INSTALL_DIR})
