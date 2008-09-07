dnl Check for GMime Library.

AC_DEFUN([AC_CHECK_GMIME], [
  have_gmime="no"
  ac_gmime="no"

  # exported variables
  GMIME_LIBS=""
  GMIME_CFLAGS=""

  AC_ARG_WITH(gmime, AC_HELP_STRING([--with-gmime[=dir]], [Compile with libgmime at given dir]),
      [ ac_gmime="$withval" 
        if test "x$withval" != "xno" -a "x$withval" != "xyes"; then
            ac_gmime="yes"
            ac_gmime_dir=$withval
        fi ],
      [ ac_gmime="auto" ] )

  if test "x$PKG_CONFIG" != "xno"; then
    if test "x$ac_gmime" = "xyes"; then
      ac_gmime_tmppkgconfig=$PKG_CONFIG_PATH
      export PKG_CONFIG_PATH=${ac_gmime_dir}/lib/pkgconfig/
      echo Setting path to $PKG_CONFIG_PATH
      PKG_CHECK_MODULES(GMIME, gmime-2.4, ,
      [
        PKG_CHECK_MODULES(GMIME, gmime-2.0, ac_gmime="no")
      ])
      export PKG_CONFIG_PATH=$ac_gmime_tmppkgconfig
    fi
  
    if test "x$ac_gmime" = "xauto"; then
      PKG_CHECK_MODULES(GMIME, gmime-2.4, ,
      [
        PKG_CHECK_MODULES(GMIME, gmime-2.0, ac_gmime="no")
      ])
    fi
  fi

  AC_MSG_RESULT([$ac_gmime])
])
