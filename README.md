# gRunner

https://github.com/user-attachments/assets/c9ba9df4-74fe-4328-a528-bdd219c55836

## What is gRunner?

gRunner is a parallel test case runner written in go, which compiles and runs each test case in an easy to view way in parallel.
Designed and written for CS439H test cases. General configurability is planned.

---

```
Usage: grunner [options] [... test files/directories]
Runs test files in the given directories or files. Multiple directories or files can be given.

Options:
  -h, --help             show this help message
  -n, --iterations int   number of iterations to execute (default 1)
  -T, --threads int      maximum number of concurrent threads to use (default CPUThreads/4)
  -e, --earlyexit        exit iterating early if a test fails
  -t, --timeout int      max time an iteration will run until being killed (default 10)
  -c, --timecap float    cap total execution time to n seconds (useful with -n) (default unlimited)
  -v, --verbose          show error information for test failures
```
