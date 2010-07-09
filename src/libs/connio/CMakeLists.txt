StrictCompile()

add_library(bongoconnio SHARED
	sockets.c
	connio.c
	trace.c
	addrpool.c
	unix-ip.c
	nmap.c
)

target_link_libraries(bongoconnio
	${GNUTLS_LIBRARIES}
)

install(TARGETS bongoconnio DESTINATION ${LIB_INSTALL_DIR})
