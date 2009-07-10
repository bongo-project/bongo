
add_library(bongoconnio SHARED
	connio.c
	trace.c
	addrpool.c
	sockets.c
	unix-ip.c
)

target_link_libraries(bongoconnio
	${GNUTLS_LIBRARIES}
)

install(TARGETS bongoconnio DESTINATION ${LIB_INSTALL_DIR})
