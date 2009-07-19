StrictCompile()

add_library(bongocal SHARED
	bongo-cal-object.c
	bongo-cal-instance.c
	bongo-cal-recur.c
	bongo-cal-time.c
	bongo-cal-timezone.c
	cal-convert.c)

# FIXME: this compile is a bit broken.
#add_executable(bongo-import-tz 
#	bongo-import-tz.c)
#
#target_link_libraries(bongo-import-tz
#	bongomemmgr
#	bongocal
#	${GLIB2_LIBRARIES}
#	${ICAL_LIBRARIES}
#	)

target_link_libraries(bongocal
	bongoxpl
	bongoutil
	bongomemmgr
	${LIBICAL_LIBRARIES}
)

install(TARGETS bongocal DESTINATION ${LIB_INSTALL_DIR})
