add_executable(bongo-testtool
	testtool.c
)

target_link_libraries(bongo-testtool
	bongoconnio
	bongomsgapi
	bongojson
	bongoutil
	bongoxpl
	bongocal
)

install(TARGETS bongo-testtool DESTINATION ${SBIN_INSTALL_DIR})
