add_executable(bongopop3
	pop3.c
)

target_link_libraries(bongopop3
	bongoxpl
	bongoconnio
	bongoutil
	bongojson
	bongomsgapi
)

install(TARGETS bongopop3 DESTINATION ${SBIN_INSTALL_DIR})
