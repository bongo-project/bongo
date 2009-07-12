StrictCompile()

enable_language(ASM-ATT)

add_executable(bongostore
	auth.c
	calendar.c
	createstore.s
	command.c
	command-parsing.c
	config.c
	cookie.c
	db.c
	fairlock.c
	guid.c
	mail.c
	maildir.c
	main.c
	mime.c
	object-model.c
	properties.c
	query-builder.c
	query-parser.c
	search.c
	store.c
	stored.c
	watch.c)

target_link_libraries(bongostore
	bongomemmgr
	bongoxpl
	bongoconnio
	bongoutil
	bongojson
	bongomsgapi
	bongologging
	${SQLITE_LIBRARIES}
	${GLIB2_LIBRARIES}
	${GCRYPT_LIBRARIES}
	${ICAL_LIBRARIES}
	${GMIME2_LIBRARIES}
	)

install(TARGETS bongostore DESTINATION ${SBIN_INSTALL_DIR})
