dnl CLucene detection m4
dnl Based on version in clucene-contribs
AC_DEFUN([ACX_CLUCENE], [
AC_LANG_SAVE
AC_LANG(C++)


# Ask user for path to clucene-core stuff:.
AC_ARG_WITH(clucene,
	AC_HELP_STRING([ --with-clucene=<path>],
		[prefix of CLucene-Core installation. e.g. /usr/local or /usr]),,)

#try some default locations
if test -z "$with_clucene"; then
	#use parent of this directory, and some common library paths
	with_clucene=`cd $top_srcdir/.. && pwd`
	with_clucene="$with_clucene /usr /usr/local /opt/sfw /opt/csw /usr/pkg"
else
	#use an absolute path
	with_clucene=`cd $with_lucene && pwd`
fi
clucene_set_failed=

AC_MSG_CHECKING([how to include clucene])
CLUCENE_CXXFLAGS=
CLUCENE_LIBS=
for flag in $with_clucene; do
	if test -z "$clucene_set_failed"; then
		if test -e "$flag/include/CLucene/StdHeader.h"; then
			config_path=""
			if test -e "$flag/include/CLucene/clucene-config.h"; then
				config_path="$flag/include/"
			fi
			if test -e "$flag/lib/CLucene/clucene-config.h"; then
				config_path="$flag/lib/"
			fi
			if test "x$config_path" != "x"; then
				if test -e "$flag/lib/libclucene.so"; then
					CLUCENE_LIBS="-L$flag/lib -lclucene"
					CLUCENE_CXXFLAGS="-I$flag/include -I$config_path"
					clucene_ver=`$AWK '{ if ($ 1 == "#define" && $ 2 == "_CL_VERSION") print $ 3; }' < "$config_path/CLucene/clucene-config.h" | tr -d '"'`
					
					echo Lucene ver: $clucene_ver
					clucene_set_failed=$flag
				fi
			fi
		fi
	fi

	if test -z "$clucene_set_failed"; then
		if test -e "$flag/src/CLucene/StdHeader.h"; then
			if test -e "$flag/src/libclucene.la"; then
				CLUCENE_LIBS="-L$flag/src/ -lclucene"
				CLUCENE_CXXFLAGS="-I$flag/src/"
				clucene_set_failed=$flag/src/
			fi
		fi
	fi
done

if test -z "$clucene_set_failed"; then
	clucene_set_failed="true"
	AC_MSG_RESULT("failed")
else
	AC_MSG_RESULT($clucene_set_failed)
fi

if test -z "$clucene_ver"; then
	# optimistic guess :D
	clucene_ver="1.0.0"
fi
AC_MSG_RESULT("Lucene ver: $clucene_ver")
CLUCENE_BONGO_API=`echo "$clucene_ver" | $AWK -F. '{
	if ($ 1 > 0 || $ 2 > 9 || ($ 2 == 9 && $ 3 > 18)) {
		print 2;
	} else {
		print 1;
	}
}'`

if test $CLUCENE_BONGO_API = 1; then
	echo Old Lucene API - Lucene Ver: $clucene_ver
else
	echo Current Lucene API - $clucene_ver
fi

AC_SUBST(CLUCENE_BONGO_API)
AC_SUBST(CLUCENE_LIBS)
AC_SUBST(CLUCENE_CXXFLAGS)

AC_LANG_RESTORE

])dnl ACX_CLUCENE
