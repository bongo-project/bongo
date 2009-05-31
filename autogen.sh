#!/bin/sh
# Run this to generate all the initial makefiles, etc.

srcdir=`dirname $0`
test -z "$srcdir" && srcdir=.

ORIGDIR=`pwd`
cd $srcdir
PROJECT=Bongo

DIE=0

# some terminal codes ...
boldface="`tput bold 2>/dev/null`"
normal="`tput sgr0 2>/dev/null`"
printbold() {
	echo "$boldface$@$normal"
}    
printerr() {
	echo "$@" >&2
}

have_libtool=false
LIBTOOLIZE=libtoolize
if $LIBTOOLIZE --version < /dev/null > /dev/null 2>&1 ; then
	libtool_version=`libtoolize --version | sed 's/^[^0-9]*\([0-9.][0-9.]*\).*/\1/'`
	case $libtool_version in 1.4*|1.5*|2.2*)
		have_libtool=true
		;;
	esac
fi
if $have_libtool ; then : ; else
	echo
	echo "You must have libtool 1.4 installed to compile $PROJECT."
	echo "Install the appropriate package for your distribution,"
	echo "or get the source tarball at http://ftp.gnu.org/gnu/libtool/"
	DIE=1
fi

(autoconf --version) < /dev/null > /dev/null 2>&1 || {
	echo
	echo "You must have autoconf installed to compile $PROJECT."
	echo "Install the appropriate package for your distribution,"
	echo "or get the source tarball at http://ftp.gnu.org/gnu/autoconf/"
	DIE=1
}

if automake-1.8 --version < /dev/null > /dev/null 2>&1 ; then
	AUTOMAKE=automake-1.8
	ACLOCAL=aclocal-1.8
elif automake-1.9 --version < /dev/null > /dev/null 2>&1 ; then
	AUTOMAKE=automake-1.9
	ACLOCAL=aclocal-1.9
elif automake-1.10 --version < /dev/null > /dev/null 2>&1 ; then
	AUTOMAKE=automake-1.10
	ACLOCAL=aclocal-1.10
else
	echo
	echo "You must have automake 1.8.x installed to compile $PROJECT."
	echo "Install the appropriate package for your distribution,"
	echo "or get the source tarball at http://ftp.gnu.org/gnu/automake/"
	DIE=1
fi

# need to check these binaries exist...
AUTOCONF=autoconf
AUTOHEADER=autoheader

if test "$DIE" -eq 1; then
	exit 1
fi

test -f configure.ac || {
	echo "You must run this script in the top-level $PROJECT directory"
	exit 1
}

if test -z "$AUTOGEN_SUBDIR_MODE"; then
	if test -z "$*"; then
		echo "I am going to run ./configure with no arguments - if you wish "
		echo "to pass any to it, please specify them on the $0 command line."
	fi
fi

$ACLOCAL $ACLOCAL_FLAGS -I m4 -I macros || exit $?

$LIBTOOLIZE --force || exit $?

$AUTOHEADER || exit $?

$AUTOMAKE --add-missing || exit $?
autoconf || exit $?
cd $ORIGDIR || exit $?

SVNREV="`svnversion . 2>/dev/null`"
if test x$SVNREV = x; then
	echo Unable to discern build version
	echo \#define  BONGO_BUILD_BRANCH	\"unknown\" >  ./include/bongo-buildinfo.h
	echo \#define  BONGO_BUILD_VSTR	\"\" >>  ./include/bongo-buildinfo.h
	echo \#define  BONGO_BUILD_VER	\"0\" >>  ./include/bongo-buildinfo.h
else
	echo SVN Rev at $SVNREV
	echo \#define  BONGO_BUILD_BRANCH	\"trunk\"   >  ./include/bongo-buildinfo.h
	echo \#define  BONGO_BUILD_VSTR	\"r\" >>  ./include/bongo-buildinfo.h
	echo \#define  BONGO_BUILD_VER	\"$SVNREV\" >> ./include/bongo-buildinfo.h
fi

# configure sub-projects
configure_files="`find $srcdir -name 'bongo-*' -prune -name '{arch}' -prune -o -name configure.ac -print -o -name configure.in -print`"
for arg in $@; do
	case $arg in
		--with-sqlite3=*import*)
			rm -f import/sqlite3/NO-AUTO-GEN
			;;
		--with-sqlite3=*)
			touch import/sqlite3/NO-AUTO-GEN
			echo "Flagging import/sqlite3 as no auto-gen as source is external"
			;;
	esac
done

topdir=`pwd`
for configure_ac in $configure_files; do 
	dirname=`dirname $configure_ac`
	basename=`basename $configure_ac`
	if test -f $dirname/NO-AUTO-GEN; then
		echo skipping $dirname -- flagged as no auto-gen
	else
		printbold "Processing $configure_ac"
		cd $dirname

		# Note that the order these tools are called should match what
		# autoconf's "autoupdate" package does.  See bug 138584 for
		# details.

		# programs that might install new macros get run before aclocal
		if grep "^A[CM]_PROG_LIBTOOL" $basename >/dev/null; then
			printbold "Running $LIBTOOLIZE..."
			$LIBTOOLIZE --force || exit 1
		fi

		if grep "^AM_GLIB_GNU_GETTEXT" $basename >/dev/null; then
			printbold "Running $GLIB_GETTEXTIZE... Ignore non-fatal messages."
			echo "no" | $GLIB_GETTEXTIZE --force --copy || exit 1
		elif grep "^AM_GNU_GETTEXT" $basename >/dev/null; then
			if grep "^AM_GNU_GETTEXT_VERSION" $basename > /dev/null; then
				printbold "Running autopoint..."
				autopoint --force || exit 1
			else
				printbold "Running $GETTEXTIZE... Ignore non-fatal messages."
				echo "no" | $GETTEXTIZE --force --copy --no-changelog || exit 1
			fi
		fi

		if grep "^AC_PROG_INTLTOOL" $basename >/dev/null; then
			printbold "Running $INTLTOOLIZE..."
			$INTLTOOLIZE --force --automake || exit 1
		fi

		# Now run aclocal to pull in any additional macros needed
		aclocalinclude="$ACLOCAL_FLAGS"
		if test -d m4; then
			aclocalinclude="$aclocalinclude -I m4"
		fi
		if test -d macros; then
			aclocalinclude="$aclocalinclude -I macros"
		fi

		printbold "Running $ACLOCAL $aclocalinclude ..."
		
		$ACLOCAL $aclocalinclude || exit 1

		# Now that all the macros are sorted, run autoconf and autoheader ...
		printbold "Running $AUTOCONF..."
		$AUTOCONF || exit 1
		if grep "^A[CM]_CONFIG_HEADER" $basename >/dev/null; then
			printbold "Running $AUTOHEADER..."
			$AUTOHEADER || exit 1
			# this prevents automake from thinking config.h.in is out of
			# date, since autoheader doesn't touch the file if it doesn't
			# change.
			test -f config.h.in && touch config.h.in
		fi

		if test -f Makefile.am; then
			# Finally, run automake to create the makefiles ...
			printbold "Running $AUTOMAKE..."
			$AUTOMAKE --gnu --add-missing || exit 1
		fi

		cd "$topdir"
	fi
done
# end configure sub-projects

if test -z "$AUTOGEN_SUBDIR_MODE"; then
	$srcdir/configure --enable-maintainer-mode $AUTOGEN_CONFIGURE_ARGS "$@" || exit $?
	
	echo 
	echo "Now type 'make' to compile $PROJECT."
fi

