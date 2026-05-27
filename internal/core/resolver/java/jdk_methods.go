package java

// jdkPackageMap maps short class name → package path for common JDK classes.
// Used by BuildExternalQualifiedName to construct fully qualified external node IDs.
var jdkPackageMap = map[string]string{
	"Stream":             "java.util.stream",
	"IntStream":          "java.util.stream",
	"LongStream":         "java.util.stream",
	"DoubleStream":       "java.util.stream",
	"Collectors":         "java.util.stream",
	"Optional":           "java.util",
	"List":               "java.util",
	"ArrayList":          "java.util",
	"LinkedList":         "java.util",
	"Map":                "java.util",
	"HashMap":            "java.util",
	"Set":                "java.util",
	"HashSet":            "java.util",
	"Collection":         "java.util",
	"Collections":        "java.util",
	"Iterator":           "java.util",
	"Arrays":             "java.util",
	"Objects":            "java.util",
	"String":             "java.lang",
	"StringBuilder":      "java.lang",
	"StringBuffer":       "java.lang",
	"Integer":            "java.lang",
	"Long":               "java.lang",
	"Double":             "java.lang",
	"Boolean":            "java.lang",
	"Object":             "java.lang",
	"Class":              "java.lang",
	"System":             "java.lang",
	"Math":               "java.lang",
	"CompletableFuture":  "java.util.concurrent",
	"ExecutorService":    "java.util.concurrent",
	"Future":             "java.util.concurrent",
	"Files":              "java.nio.file",
	"Path":               "java.nio.file",
	"Paths":              "java.nio.file",
}

// jdkTypeHierarchy maps className → direct parent types for common JDK classes.
// Used by isJDKSubtype to check type compatibility for overload disambiguation.
var jdkTypeHierarchy = map[string][]string{
	// Throwable hierarchy
	"Throwable":                        {"Object"},
	"Exception":                        {"Throwable"},
	"RuntimeException":                 {"Exception"},
	"Error":                            {"Throwable"},
	"IOException":                      {"Exception"},
	"SQLException":                     {"Exception"},
	"NullPointerException":             {"RuntimeException"},
	"IllegalArgumentException":         {"RuntimeException"},
	"IllegalStateException":            {"RuntimeException"},
	"ClassCastException":               {"RuntimeException"},
	"IndexOutOfBoundsException":        {"RuntimeException"},
	"ArrayIndexOutOfBoundsException":   {"IndexOutOfBoundsException"},
	"StringIndexOutOfBoundsException":  {"IndexOutOfBoundsException"},
	"UnsupportedOperationException":    {"RuntimeException"},
	"ConcurrentModificationException":  {"RuntimeException"},
	"NumberFormatException":            {"IllegalArgumentException"},
	"ArithmeticException":              {"RuntimeException"},
	"SecurityException":                {"RuntimeException"},
	"NoSuchElementException":           {"RuntimeException"},
	"EmptyStackException":              {"RuntimeException"},
	"TypeNotPresentException":          {"RuntimeException"},
	"DateTimeException":                {"RuntimeException"},
	"DateTimeParseException":           {"DateTimeException"},
	"BufferOverflowException":          {"RuntimeException"},
	"BufferUnderflowException":         {"RuntimeException"},
	"CompletionException":              {"RuntimeException"},
	"RejectedExecutionException":       {"RuntimeException"},
	"CancellationException":            {"IllegalStateException"},
	"FileNotFoundException":            {"IOException"},
	"InterruptedException":             {"Exception"},
	"TimeoutException":                 {"Exception"},
	"ExecutionException":               {"Exception"},
	"ClassNotFoundException":           {"Exception"},
	"NoSuchMethodException":            {"Exception"},
	"NoSuchFieldException":             {"Exception"},
	"CloneNotSupportedException":       {"Exception"},
	"ParseException":                   {"Exception"},
	"MalformedURLException":            {"IOException"},
	"SocketException":                  {"IOException"},
	"ConnectException":                 {"SocketException"},
	"SocketTimeoutException":           {"IOException"},
	"EOFException":                     {"IOException"},
	"OutOfMemoryError":                 {"Error"},
	"StackOverflowError":               {"Error"},
	"AssertionError":                   {"Error"},

	// Number hierarchy
	"Number":     {"Object"},
	"Integer":    {"Number"},
	"Long":       {"Number"},
	"Double":     {"Number"},
	"Float":      {"Number"},
	"Short":      {"Number"},
	"Byte":       {"Number"},
	"BigDecimal": {"Number"},
	"BigInteger": {"Number"},

	// String / CharSequence
	"String":        {"CharSequence", "Comparable", "Object"},
	"StringBuilder": {"CharSequence", "Object"},
	"StringBuffer":  {"CharSequence", "Object"},
	"CharSequence":  {"Object"},

	// Collection hierarchy
	"Iterable":   {"Object"},
	"Collection": {"Iterable"},
	"List":       {"Collection"},
	"Set":        {"Collection"},
	"SortedSet":  {"Set"},
	"NavigableSet": {"SortedSet"},
	"Queue":      {"Collection"},
	"Deque":      {"Queue"},
	"ArrayList":  {"List"},
	"LinkedList": {"List", "Deque"},
	"HashSet":    {"Set"},
	"TreeSet":    {"NavigableSet"},
	"LinkedHashSet": {"HashSet"},
	"Vector":     {"List"},
	"Stack":      {"Vector"},
	"PriorityQueue": {"Queue"},
	"ArrayDeque": {"Deque"},
	"CopyOnWriteArrayList": {"List"},

	// Map hierarchy
	"Map":               {"Object"},
	"SortedMap":         {"Map"},
	"NavigableMap":      {"SortedMap"},
	"HashMap":           {"Map"},
	"LinkedHashMap":     {"HashMap"},
	"TreeMap":           {"NavigableMap"},
	"ConcurrentHashMap": {"Map"},
	"Hashtable":         {"Map"},
	"WeakHashMap":       {"Map"},
	"EnumMap":           {"Map"},

	// Boolean / Character / Void
	"Boolean":   {"Comparable", "Object"},
	"Character": {"Comparable", "Object"},
	"Void":      {"Object"},
	"Comparable": {"Object"},

	// Stream / Optional / Future
	"Stream":             {"Object"},
	"Optional":           {"Object"},
	"Future":             {"Object"},
	"CompletableFuture":  {"Future"},

	// Date / Time
	"Date":          {"Comparable", "Object"},
	"Calendar":      {"Comparable", "Object"},
	"Instant":       {"Comparable", "Object"},
	"LocalDate":     {"Comparable", "Object"},
	"LocalDateTime": {"Comparable", "Object"},
	"LocalTime":     {"Comparable", "Object"},
	"ZonedDateTime": {"Object"},
	"Duration":      {"Comparable", "Object"},
	"Period":        {"Object"},
	"DayOfWeek":     {"Enum"},
	"ZoneId":        {"Object"},
	"DateTimeFormatter": {"Object"},

	// IO
	"InputStream":       {"Object"},
	"OutputStream":      {"Object"},
	"Reader":            {"Object"},
	"Writer":            {"Object"},
	"FileInputStream":   {"InputStream"},
	"FileOutputStream":  {"OutputStream"},
	"BufferedReader":     {"Reader"},
	"BufferedWriter":     {"Writer"},
	"InputStreamReader":  {"Reader"},
	"OutputStreamWriter": {"Writer"},
	"PrintWriter":        {"Writer"},
	"PrintStream":        {"OutputStream"},
	"ByteArrayInputStream":  {"InputStream"},
	"ByteArrayOutputStream": {"OutputStream"},
	"StringReader":       {"Reader"},
	"StringWriter":       {"Writer"},
	"File":               {"Comparable", "Object"},
	"Path":               {"Comparable", "Object"},

	// Thread / Runnable
	"Thread":   {"Runnable", "Object"},
	"Runnable": {"Object"},
	"Callable": {"Object"},

	// Enum
	"Enum": {"Comparable", "Object"},

	// Class / Type
	"Class": {"Object"},
	"Type":  {"Object"},

	// Pattern / Matcher
	"Pattern": {"Object"},
	"Matcher": {"Object"},

	// UUID
	"UUID": {"Comparable", "Object"},
}

// isJDKSubtype checks if child is a subtype of parent using the JDK hierarchy table.
func isJDKSubtype(child, parent string) bool {
	if child == parent {
		return true
	}
	ancestors, ok := jdkTypeHierarchy[child]
	if !ok {
		return false
	}
	for _, a := range ancestors {
		if a == parent {
			return true
		}
		if isJDKSubtype(a, parent) {
			return true
		}
	}
	return false
}

// jdkTypeDepth returns the inheritance depth from a type to a target parent.
// Returns -1 if not a subtype. Used to select the most specific overload.
func jdkTypeDepth(child, parent string) int {
	if child == parent {
		return 0
	}
	ancestors, ok := jdkTypeHierarchy[child]
	if !ok {
		return -1
	}
	best := -1
	for _, a := range ancestors {
		if a == parent {
			return 1
		}
		d := jdkTypeDepth(a, parent)
		if d >= 0 && (best < 0 || d+1 < best) {
			best = d + 1
		}
	}
	return best
}
