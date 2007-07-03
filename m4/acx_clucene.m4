dnl CLucene detection m4
dnl Loosely based on version in clucene-contribs


AC_DEFUN([AC_FIND_FILE], [
  $3=no
  for i in $2; do
      for j in $1; do
          if test -r "$i/$j"; then
              $3=$i
              break 2
          fi
      done
  done ])

AC_DEFUN([ACX_CLUCENE], [
AC_LANG_SAVE
AC_LANG(C++)

  have_clucene="no"
  ac_clucene="no"
  ac_clucene_incdir="no"
  ac_clucene_libdir="no"

  clucene_ver=""

  # exported variables
  CLUCENE_LIBS=""
  CLUCENE_CXXFLAGS=""
  CLUCENE_BONGO_API=""

  AC_ARG_WITH(clucene,		AC_HELP_STRING([--with-clucene[=dir]],	[Compile with CLucene at given dir]),
      [ ac_clucene="$withval" 
        if test "x$withval" != "xno" -a "x$withval" != "xyes"; then
            ac_clucene="yes"
            ac_clucene_incdir="$withval"/include
            ac_clucene_libdir="$withval"/lib
        fi ],, )
  AC_ARG_WITH(clucene-incdir,	AC_HELP_STRING([--with-clucene-incdir],	[Specifies where the CLucene include files are.]),
      [  ac_clucene_incdir="$withval" ] )
  AC_ARG_WITH(clucene-libdir,	AC_HELP_STRING([--with-clucene-libdir],	[Specifies where the CLucene libraries are.]),
      [  ac_clucene_libdir="$withval" ] )

  AC_MSG_CHECKING([for CLucene])

  clucene_incdirs="/usr/include /usr/local/include /opt/sfw/include /opt/csw/include /usr/pkg/include"
  clucene_libdirs="/usr/lib /usr/local/lib /opt/sfw/lib /opt/csw/lib /usr/pkg/lib"
  clucene_alldirs="$clucene_libdirs $clucene_incdirs"

  if test "$ac_clucene_incdir" = "no"; then
      AC_FIND_FILE(CLucene/StdHeader.h, $clucene_incdirs, ac_clucene_incdir)
  fi
  
  if test "$ac_clucene_libdir" = "no"; then
      AC_FIND_FILE(libclucene.so, $clucene_libdirs, ac_clucene_libdir)
  fi

  AC_FIND_FILE(CLucene/clucene-config.h, $clucene_alldirs, ac_clucene_confdir)
  
  if test "x$ac_clucene_incdir" != "xno" -a "x$ac_clucene_libdir" != "xno" -a "x$ac_clucene_confdir" != "xno"; then
      have_clucene="yes"
  fi

  if test "x$have_clucene" = "xyes"; then
      clucene_ver=`$AWK '{ if ($ 1 == "#define" && $ 2 == "_CL_VERSION") print $ 3; }' < "$ac_clucene_confdir/CLucene/clucene-config.h" | tr -d '"'`

      if test -z "$clucene_ver"; then
        # optimistic guess :D
        clucene_ver="1.0.0"
      fi

      CLUCENE_BONGO_API=`echo "$clucene_ver" | $AWK -F. '{
        if ($ 1 > 0 || $ 2 > 9 || ($ 2 == 9 && $ 3 > 18)) {
          print 2;
        } else {
          print 1;
        }
      }'`

      if test x"$ac_clucene_libdir" = xno; then
          test "x$CLUCENE_LIBS" = "x" && CLUCENE_LIBS="-lclucene"
      else
          test "x$CLUCENE_LIBS" = "x" && CLUCENE_LIBS="-L$ac_clucene_libdir -lclucene"
      fi

      test x"$ac_clucene_incdir" != xno && test "x$CLUCENE_CXXFLAGS" = "x" && CLUCENE_CXXFLAGS="-I$ac_clucene_incdir -I$ac_clucene_libdir"

      AC_SUBST(CLUCENE_LIBS)
      AC_SUBST(CLUCENE_CXXFLAGS)
      AC_SUBST(CLUCENE_BONGO_API)
  fi

  AC_MSG_RESULT([$have_clucene])

  AC_MSG_CHECKING([CLucene API])

  if test "x$CLUCENE_BONGO_API" = "x"; then
    AC_MSG_RESULT([Using Imported CLucene: Old API])
  else if test $CLUCENE_BONGO_API = 1; then
    AC_MSG_RESULT([Old API - CLucene Ver: $clucene_ver])
  else
    AC_MSG_RESULT([Current API - CLucene Ver: $clucene_ver])
  fi; fi

AC_LANG_RESTORE

])dnl ACX_CLUCENE
