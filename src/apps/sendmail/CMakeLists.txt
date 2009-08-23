add_executable(bongo-sendmail
    sendmail.c
)

target_link_libraries(bongo-sendmail
    bongomsgapi
    bongojson
    bongoutil
    bongoconnio
)

install(TARGETS bongo-sendmail DESTINATION ${SBIN_INSTALL_DIR})
