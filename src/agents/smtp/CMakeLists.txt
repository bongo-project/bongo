#StrictCompile()

add_executable(bongosmtp
	smtpd.c
)

target_link_libraries(bongosmtp
	bongoxpl
	bongoconnio
	bongoutil
	bongojson
	bongomsgapi
)

add_executable(bongosmtpc
	smtpc.c
)

target_link_libraries(bongosmtpc
	bongoxpl
	bongoconnio
	bongoutil
	bongojson
	bongomsgapi
)

install(TARGETS bongosmtp DESTINATION ${SBIN_INSTALL_DIR})
install(TARGETS bongosmtpc DESTINATION ${SBIN_INSTALL_DIR})
