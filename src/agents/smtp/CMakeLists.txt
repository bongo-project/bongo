add_executable(bongosmtp
	smtpd.c
)

target_link_libraries(bongosmtp
	bongomemmgr
	bongoxpl
	bongoconnio
	bongoutil
	bongojson
	bongomsgapi
	bongologging
)

add_executable(bongosmtpc
	smtpc.c
)

target_link_libraries(bongosmtpc
	bongomemmgr
	bongoxpl
	bongoconnio
	bongoutil
	bongojson
	bongomsgapi
	bongologging
)

install(TARGETS bongosmtp DESTINATION ${SBIN_INSTALL_DIR})
install(TARGETS bongosmtpc DESTINATION ${SBIN_INSTALL_DIR})
