add_executable(bongoimap
	imapd.c
	copy.c
	event.c
	fetch.c
	progress.c
	store.c
	search.c
	session.c
	rfc822parse.c
	uid.c
)

target_link_libraries(bongoimap
	bongoxpl
	bongoconnio
	bongoutil
	bongojson
	bongomsgapi
	bongologging
)

install(TARGETS bongoimap DESTINATION ${SBIN_INSTALL_DIR})
