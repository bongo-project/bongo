add_executable(bongo-backup
    backup.c
)

target_link_libraries(bongo-backup
    bongomsgapi
    bongojson
    bongoutil
    bongoconnio
)

install(TARGETS bongo-backup DESTINATION ${SBIN_INSTALL_DIR})
