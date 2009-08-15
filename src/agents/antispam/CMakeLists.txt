StrictCompile()

add_executable(bongoantispam
	antispam.c
	spamd.c
)

target_link_libraries(bongoantispam
	bongoxpl
	bongoconnio
	bongoutil
	bongojson
	bongomsgapi
	bongologging
)

install(TARGETS bongoantispam DESTINATION ${SBIN_INSTALL_DIR})
