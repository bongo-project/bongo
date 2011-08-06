StrictCompile()

enable_language(ASM-ATT)

add_executable(bongostore
	accounts.c
	auth.c
	calendar.c
	sql/createstore.s
	sql/createcookie-1.s
	command.c
	command-parsing.c
	contacts.c
	config.c
	cookie.c
	db.c
	fairlock.c
	guid.c
	locking.c
	mail.c
	maildir.c
	main.c
	mime.c
	object-model.c
	object-queries.c
	properties.c
	query-builder.c
	query-parser.c
	ringlog.c
	search.c
	store.c
	stored.c
	watch.c)

target_link_libraries(bongostore
	bongostreamio
	bongoxpl
	bongoconnio
	bongoutil
	bongojson
	bongomsgapi
	${SQLITE_LIBRARIES}
	${GLIB2_LIBRARIES}
	${GCRYPT_LIBRARIES}
	${ICAL_LIBRARIES}
	${GMIME2_LIBRARIES}
	)

install(TARGETS bongostore DESTINATION ${SBIN_INSTALL_DIR})
